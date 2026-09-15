"""OpenTelemetry 指标（Copilot 侧）：对话轮次/工具调用/活跃流经 OTLP 推送。

与 tracing 同一套开关：TP_OTEL_EXPORTER（"" 关闭（默认）| "stdout" | "otlp"）、
TP_OTEL_ENDPOINT。关闭时打点走 API 层 no-op 实现，零开销。
Copilot 的对外 8100 仅调试暴露（生产经 Scheduler 反代），同样采用 OTLP 周期
推送（15s），由部署层 OTel Collector 转 Prometheus 格式暴露。
"""

from __future__ import annotations

import asyncio
import logging
import os
import time

from opentelemetry import metrics as otel_metrics
from opentelemetry.sdk.metrics import MeterProvider
from opentelemetry.sdk.metrics.export import ConsoleMetricExporter, PeriodicExportingMetricReader
from opentelemetry.sdk.metrics.view import ExplicitBucketHistogramAggregation, View
from opentelemetry.sdk.resources import Resource

log = logging.getLogger("testpilot.copilot")

# 模块导入时经 ProxyMeter 获取；init() 设置真实 Provider 后自动委托（与 tracing 同模式）。
meter = otel_metrics.get_meter("testpilot.copilot")

# 对话轮次（每条 /api/chat 请求）：result=rejected（流建立前的 4xx/5xx 短响应）/
# ok（流正常结束）/ cancelled（客户端断开或 idle 超时取消）/ error（流内异常）。
CHAT_TURNS = meter.create_counter(
    "testpilot.copilot.chat_turns", unit="{turn}",
    description="对话轮次计数（按结果：rejected/ok/cancelled/error）。")
_CHAT_DURATION_NAME = "testpilot.copilot.chat_duration"  # proxy instrument 无 .name，View 按名匹配
CHAT_DURATION = meter.create_histogram(
    _CHAT_DURATION_NAME, unit="s",
    description="对话轮次时长（请求进入到流结束；rejected 为短响应耗时）。")
ACTIVE_STREAMS = meter.create_up_down_counter(
    "testpilot.copilot.active_streams", unit="{stream}",
    description="当前活跃的 SSE 对话流数。")

# 工具调用（含审批型：仅在批准后实际执行时计数，拒绝不执行不计）。
TOOL_CALLS = meter.create_counter(
    "testpilot.copilot.tool_calls", unit="{call}",
    description="工具调用计数（tool=工具名，result=ok/error）。")
_TOOL_DURATION_NAME = "testpilot.copilot.tool_duration"  # 同上
TOOL_DURATION = meter.create_histogram(
    _TOOL_DURATION_NAME, unit="s",
    description="工具执行时长。")

# 直方图桶边界必须定制：SDK 默认桶 (0,5,10,…,10000) 是毫秒刻度，与本处记录的
# 秒不匹配（工具调用普遍 0.01-5s 会全挤进 le=5 桶，分布不可用）。
_TOOL_DURATION_BUCKETS = [0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10]
_CHAT_DURATION_BUCKETS = [1, 5, 10, 30, 60, 120, 300, 600, 1800]  # 对齐 Scheduler RunDuration

_VIEWS = [
    View(instrument_name=_TOOL_DURATION_NAME,
         aggregation=ExplicitBucketHistogramAggregation(boundaries=_TOOL_DURATION_BUCKETS)),
    View(instrument_name=_CHAT_DURATION_NAME,
         aggregation=ExplicitBucketHistogramAggregation(boundaries=_CHAT_DURATION_BUCKETS)),
]


def init(service_name: str = "testpilot-copilot", reader=None) -> None:
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
    """冲刷未导出的指标（进程收尾调用；未初始化时 no-op）。"""
    if _provider is not None:
        _provider.shutdown(timeout_millis=timeout_millis)


def observe_turn(response, started_at: float):
    """对话轮次收尾。

    - StreamingResponse（attach 成功）：活跃流 +1，迭代结束（正常/取消/异常）
      时 -1 并记录整轮时长与结果——与 tracing.attach_stream_end 同机制，
      包在其外层（trace 迭代器之内、业务迭代器之外）。
    - 短响应（错误 JSON 等）：记 result=rejected，时长为 handler 耗时。
    """
    it = getattr(response, "body_iterator", None)
    if it is None:
        CHAT_TURNS.add(1, {"result": "rejected"})
        CHAT_DURATION.record(time.monotonic() - started_at, {"result": "rejected"})
        return
    ACTIVE_STREAMS.add(1)
    response.body_iterator = _observe_iter(it, started_at)


def observe_turn_failed(started_at: float, result: str) -> None:
    """handler 未捕获异常/取消（流未建立即 500）：指标必须可见，否则此类
    故障在 chat_turns 上完全隐形。result=error|cancelled 由调用方区分。"""
    CHAT_TURNS.add(1, {"result": result})
    CHAT_DURATION.record(time.monotonic() - started_at, {"result": result})


async def _observe_iter(inner, started_at: float):
    result = "ok"
    try:
        async for chunk in inner:
            yield chunk
    except (asyncio.CancelledError, GeneratorExit):
        result = "cancelled"
        raise
    except Exception:
        result = "error"
        raise
    finally:
        ACTIVE_STREAMS.add(-1)
        CHAT_TURNS.add(1, {"result": result})
        CHAT_DURATION.record(time.monotonic() - started_at, {"result": result})
        if result == "error":
            log.warning("chat stream ended with error after %.1fs",
                        time.monotonic() - started_at)
