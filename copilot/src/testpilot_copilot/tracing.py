"""OpenTelemetry 链路（Copilot 侧）：入站 HTTP 起 span，出站 gRPC 注入 traceparent。

环境变量同 Worker：TP_OTEL_EXPORTER（""=关闭 | stdout | otlp）、TP_OTEL_ENDPOINT。
关闭时中间件/拦截器仍工作（无 SDK 时传播为 no-op，trace_id 字段为空）。
"""

from __future__ import annotations

import logging
import os

import grpc.aio
from opentelemetry import trace
from opentelemetry.propagate import extract as _otel_extract
from opentelemetry.propagate import inject as _otel_inject
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor, ConsoleSpanExporter

tracer = trace.get_tracer("testpilot.copilot")


def extract_headers(headers: dict):
    """从入站 HTTP 头提取上游上下文（无 traceparent → None=开新 trace）。"""
    if not headers.get("traceparent"):
        return None
    return _otel_extract(headers)


def inject_headers(headers: dict) -> dict:
    """把当前 span 的 traceparent 注入出站 HTTP 头（无 span 时原样返回）。"""
    carrier: dict[str, str] = {}
    _otel_inject(carrier)
    if tp := carrier.get("traceparent"):
        headers = {**headers, "traceparent": tp}
    return headers


def begin_span(headers: dict, **attrs):
    """入站请求起 SERVER span 并 attach 为当前上下文；返回 (span, token)，
    调用方负责 detach + end（或转交 attach_stream_end）。"""
    from opentelemetry import context as otel_context

    span = tracer.start_span("copilot.chat", context=extract_headers(headers),
                             kind=trace.SpanKind.SERVER, attributes=attrs)
    return span, otel_context.attach(trace.set_span_in_context(span))


def detach(token) -> None:
    from opentelemetry import context as otel_context

    otel_context.detach(token)


def attach_stream_end(response, span) -> bool:
    """把 span 生命周期转交流式响应：迭代期恢复上下文，迭代结束 end。
    非流式响应返回 False（调用方自行 end）。"""
    it = getattr(response, "body_iterator", None)
    if it is None:
        return False
    response.body_iterator = _resume_iter(it, span)
    return True


async def _resume_iter(it, span):
    from opentelemetry import context as otel_context

    token = otel_context.attach(trace.set_span_in_context(span))
    try:
        async for chunk in it:
            yield chunk
    finally:
        otel_context.detach(token)
        span.end()


def init(service_name: str = "testpilot-copilot") -> None:
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


class TraceparentInterceptor(grpc.aio.UnaryUnaryClientInterceptor):
    """Copilot→Scheduler 一元 RPC：注入当前 span 的 traceparent 到 gRPC metadata。"""

    async def intercept_unary_unary(self, continuation, client_call_details, request):
        carrier: dict[str, str] = {}
        _otel_inject(carrier)
        tp = carrier.get("traceparent")
        if tp:
            md = list(client_call_details.metadata or []) + [("traceparent", tp)]
            client_call_details = client_call_details._replace(metadata=md)
        return await continuation(client_call_details, request)


class TraceLogFilter(logging.Filter):
    def filter(self, record: logging.LogRecord) -> bool:
        span = trace.get_current_span()
        sc = span.get_span_context() if span else None
        record.trace_id = format(sc.trace_id, "032x") if sc and sc.trace_id else ""
        return True


def attach_log_filter() -> None:
    f = TraceLogFilter()
    for h in logging.getLogger().handlers:
        h.addFilter(f)
