"""OpenTelemetry 链路（Worker 侧）：与 Scheduler 统一 trace_id（design 14）。

环境变量：
  TP_OTEL_EXPORTER  "" 关闭（默认）| "stdout" 开发调试 | "otlp" 发 Collector
  TP_OTEL_ENDPOINT  otlp gRPC 地址（默认 127.0.0.1:4317）

Scheduler 派发任务时在 TaskAssignment.traceparent 携带 W3C traceparent，
本模块 extract() 提取续链；LogFilter 把当前 span 的 trace_id 注入日志记录，
无 span 时字段为空字符串（格式稳定，便于 grep/采集）。
"""

from __future__ import annotations

import logging
import os

from opentelemetry import trace
from opentelemetry.propagate import extract as _otel_extract
from opentelemetry.propagate import inject as _otel_inject
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor, ConsoleSpanExporter

tracer = trace.get_tracer("testpilot.worker")


def init(service_name: str = "testpilot-worker") -> None:
    """按 TP_OTEL_EXPORTER 初始化 TracerProvider；默认关闭（零开销）。"""
    mode = os.environ.get("TP_OTEL_EXPORTER", "")
    if mode not in ("stdout", "otlp"):
        return
    if mode == "stdout":
        exporter = ConsoleSpanExporter()
    else:
        from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter

        exporter = OTLPSpanExporter(
            endpoint=os.environ.get("TP_OTEL_ENDPOINT", "127.0.0.1:4317"), insecure=True
        )
    provider = TracerProvider(resource=Resource.create({"service.name": service_name}))
    provider.add_span_processor(BatchSpanProcessor(exporter))
    trace.set_tracer_provider(provider)


def extract_context(traceparent: str):
    """从 traceparent 字符串提取上游上下文（空 → 全新 trace）。"""
    if not traceparent:
        return None
    return _otel_extract({"traceparent": traceparent})


def current_traceparent() -> str:
    """当前 span 的 traceparent（无 → ""）。"""
    carrier: dict[str, str] = {}
    _otel_inject(carrier)
    return carrier.get("traceparent", "")


def current_trace_id() -> str:
    span = trace.get_current_span()
    sc = span.get_span_context()
    return format(sc.trace_id, "032x") if sc and sc.trace_id else ""


class TraceLogFilter(logging.Filter):
    """给日志记录注入 trace_id / span_id 字段。"""

    def filter(self, record: logging.LogRecord) -> bool:
        span = trace.get_current_span()
        sc = span.get_span_context() if span else None
        record.trace_id = format(sc.trace_id, "032x") if sc and sc.trace_id else ""
        record.span_id = format(sc.span_id, "016x") if sc and sc.span_id else ""
        return True


def attach_log_filter() -> None:
    """root logger 所有 handler 挂 TraceLogFilter（basicConfig 之后调用）。"""
    root = logging.getLogger()
    f = TraceLogFilter()
    for h in root.handlers:
        h.addFilter(f)
