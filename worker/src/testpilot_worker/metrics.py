"""OpenTelemetry 指标（Worker 侧）：任务执行/探测会话/outbox 丢弃经 OTLP 推送。

与 tracing 同一套开关：TP_OTEL_EXPORTER（"" 关闭（默认）| "stdout" | "otlp"）、
TP_OTEL_ENDPOINT。关闭时打点走 API 层 no-op 实现，零开销。
Worker 无可抓取的 HTTP 面，采用 OTLP 周期推送（15s，对齐 Prometheus 抓取节奏），
由部署层的 OTel Collector 转 Prometheus 格式暴露（deploy/otel-collector.yaml）。
"""

from __future__ import annotations

import os

from opentelemetry import metrics as otel_metrics
from opentelemetry.metrics import Observation
from opentelemetry.sdk.metrics import MeterProvider
from opentelemetry.sdk.metrics.export import ConsoleMetricExporter, PeriodicExportingMetricReader
from opentelemetry.sdk.metrics.view import ExplicitBucketHistogramAggregation, View
from opentelemetry.sdk.resources import Resource

# 模块导入时经 ProxyMeter 获取；init() 设置真实 Provider 后自动委托（与 tracing 同模式）。
meter = otel_metrics.get_meter("testpilot.worker")

# 任务收尾计数：task_type=functional_declarative/functional_lowcode/playwright/stress，
# status=passed/failed/aborted/timeout/...（RUN_STATUS 枚举名）。
TASKS = meter.create_counter(
    "testpilot.worker.tasks", unit="{task}",
    description="Task finalization count (by kind and result status).")
# 指标名常量：proxy instrument 无 .name 属性，View 按名匹配须用同一字面量
_TASK_DURATION_NAME = "testpilot.worker.task.duration"
TASK_DURATION = meter.create_histogram(
    _TASK_DURATION_NAME, unit="s",
    description="Task execution duration (while holding a concurrency slot; queue wait not counted).")
ACTIVE_TASKS = meter.create_up_down_counter(
    "testpilot.worker.active_tasks", unit="{task}",
    description="Currently running tasks (holding concurrency semaphore slots).")
OUTBOX_DROPPED = meter.create_counter(
    "testpilot.worker.outbox_dropped", unit="{event}",
    description="Events dropped due to full outbox (kind=dropped discarded / evicted sacrificed for heartbeats).")
PROBE_SESSIONS = meter.create_observable_gauge(
    "testpilot.worker.probe_sessions", unit="{session}",
    description="Active UI probe sessions.",
    callbacks=[lambda _options: _observe_probe_sessions()])

# 探测会话数读取器：WorkerClient 构造后注入（gauge 回调在采集线程触发）。
_probe_sessions_getter = None


def set_probe_sessions_getter(getter) -> None:
    global _probe_sessions_getter
    _probe_sessions_getter = getter


def _observe_probe_sessions():
    get = _probe_sessions_getter
    if get is not None:
        yield Observation(get())


# TaskType 枚举 → 低基数标签（0=unspecified 归 other，防未来新增值炸标签）。
_TASK_TYPE_NAMES = {
    1: "functional_declarative",
    2: "functional_lowcode",
    3: "playwright",
    4: "stress",
}


def task_type_name(t: int) -> str:
    return _TASK_TYPE_NAMES.get(int(t), "other")


# RunStatus 枚举 → 标签（与 Scheduler 侧 metrics.RunStatusName 对齐）。
_STATUS_NAMES = {1: "running", 2: "passed", 3: "failed", 4: "aborted", 5: "timeout"}


def status_name(s: int) -> str:
    return _STATUS_NAMES.get(int(s), "other")


# 直方图桶边界必须定制：SDK 默认桶 (0,5,10,…,10000) 是毫秒刻度，与本处记录的
# 秒不匹配（典型任务 1-600s 会全挤进 le=5 桶）。对齐 Scheduler 侧 RunDuration
# 的取值（internal/metrics/metrics.go）。
_TASK_DURATION_BUCKETS = [1, 5, 10, 30, 60, 120, 300, 600, 1800]

_VIEWS = [
    View(instrument_name=_TASK_DURATION_NAME,
         aggregation=ExplicitBucketHistogramAggregation(boundaries=_TASK_DURATION_BUCKETS)),
]


def init(service_name: str = "testpilot-worker", reader=None) -> None:
    """按 TP_OTEL_EXPORTER 初始化 MeterProvider；默认关闭（no-op 打点）。

    reader 仅供测试注入（如 InMemoryMetricReader），注入时无视开关直接挂载。
    """
    readers = []
    if reader is not None:
        readers.append(reader)
    mode = os.environ.get("TP_OTEL_EXPORTER", "")
    if mode in ("stdout", "otlp"):
        if mode == "stdout":
            exporter = ConsoleMetricExporter()
        else:
            from opentelemetry.exporter.otlp.proto.grpc.metric_exporter import OTLPMetricExporter

            exporter = OTLPMetricExporter(
                endpoint=os.environ.get("TP_OTEL_ENDPOINT", "127.0.0.1:4317"), insecure=True)
        readers.append(PeriodicExportingMetricReader(exporter, export_interval_millis=15_000))
    if not readers:
        return
    provider = MeterProvider(
        resource=Resource.create({"service.name": service_name}),
        views=_VIEWS,
        metric_readers=readers,
    )
    otel_metrics.set_meter_provider(provider)
    global _provider
    _provider = provider


_provider = None


def shutdown(timeout_millis: int = 2000) -> None:
    """冲刷未导出的指标（SIGTERM 收尾调用；未初始化时 no-op）。"""
    if _provider is not None:
        _provider.shutdown(timeout_millis=timeout_millis)
