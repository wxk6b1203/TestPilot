"""metrics.py：OTel 指标打点（工具调用包装 / 对话轮次 / 活跃流）。

用 InMemoryMetricReader 注入初始化（无视 TP_OTEL_EXPORTER 开关），断言
instrument 数据点；未初始化场景（no-op 打点）由其余既有测试隐式覆盖。
"""

import asyncio
import time

import pytest
from fastapi.responses import JSONResponse
from opentelemetry.sdk.metrics.export import InMemoryMetricReader

from testpilot_copilot import metrics, tools

# 模块级共享 reader：get_metrics_data() 读走即清空，测试各自收集互不影响。
_reader = InMemoryMetricReader()
metrics.init(reader=_reader)


def _snapshot() -> dict:
    """收集当前指标快照：{指标名: {(标签项): 值}}；直方图记 (count, sum)。"""
    out: dict[str, dict] = {}
    data = _reader.get_metrics_data()
    if data is None:
        return out
    for rm in data.resource_metrics:
        for sm in rm.scope_metrics:
            for m in sm.metrics:
                slot = out.setdefault(m.name, {})
                for p in m.data.data_points:
                    labels = tuple(sorted((p.attributes or {}).items()))
                    if hasattr(p, "count"):
                        slot[labels] = (p.count, p.sum)  # HistogramDataPoint
                    else:
                        slot[labels] = slot.get(labels, 0) + p.value
    return out


def test_metered_wrapper_counts_ok_and_error():
    """_metered 包装：正常返回计 ok，抛异常计 error 且异常原样透传。"""

    async def ok_fn(ctx, q: str = "") -> list[dict]:
        return [{"name": q}]

    async def boom_fn(ctx) -> dict:
        raise ValueError("x")

    wrapped_ok = tools._metered(ok_fn)
    wrapped_err = tools._metered(boom_fn)
    assert asyncio.run(wrapped_ok(None, q="p1")) == [{"name": "p1"}]
    with pytest.raises(ValueError):
        asyncio.run(wrapped_err(None))

    snap = _snapshot()
    assert snap["testpilot.copilot.tool_calls"].get(
        (("result", "ok"), ("tool", "ok_fn")), 0) >= 1
    assert snap["testpilot.copilot.tool_calls"].get(
        (("result", "error"), ("tool", "boom_fn")), 0) >= 1
    assert snap["testpilot.copilot.tool_duration"][(("tool", "ok_fn"),)][0] >= 1


def test_toolsets_register_wrapped_tools():
    """三个工具集经 _MeteredToolset 注册：数量齐全且 schema 可解析。"""
    assert len(tools.readonly.tools) == 19  # +2：list_data_models / get_data_model
    assert len(tools.writes.tools) == 24    # +3：create/update/delete_data_model
    assert len(tools.probe.tools) == 6
    # 包装器不得破坏签名解析（__future__ annotations 的字符串求值路径）
    props = tools.writes.tools["update_api"].function_schema.json_schema["properties"]
    assert set(props) == {"api_id", "api", "kind"}
    # 新增 REST 工具同样可解析（dict | None / Any 参数不破坏 schema 生成）
    dm = tools.writes.tools["create_data_model"].function_schema.json_schema["properties"]
    assert set(dm) == {"name", "json_schema", "json", "description", "parent_node_id", "project_id"}


def test_observe_turn_rejected():
    """短响应（无 body_iterator，如鉴权失败 JSON）→ result=rejected。"""
    resp = JSONResponse({"error": "missing bearer token"}, status_code=401)
    metrics.observe_turn(resp, time.monotonic() - 0.01)
    snap = _snapshot()
    assert snap["testpilot.copilot.chat_turns"].get((("result", "rejected"),), 0) >= 1
    assert snap["testpilot.copilot.chat_duration"][(("result", "rejected"),)][0] >= 1


def test_observe_turn_stream_ok():
    """流式响应：迭代期间活跃流 +1；迭代完毕归零、result=ok、时长被记录。"""

    async def gen():
        for chunk in (b"data: 1\n\n", b"data: 2\n\n"):
            yield chunk

    class Resp:
        body_iterator = gen()

    resp = Resp()
    metrics.observe_turn(resp, time.monotonic() - 0.01)

    async def consume():
        mid = _snapshot()  # 迭代期间采集：活跃流 +1
        assert mid["testpilot.copilot.active_streams"][()] == 1
        async for _ in resp.body_iterator:
            pass

    asyncio.run(consume())
    snap = _snapshot()
    assert snap["testpilot.copilot.active_streams"][()] == 0
    assert snap["testpilot.copilot.chat_turns"].get((("result", "ok"),), 0) >= 1
    assert snap["testpilot.copilot.chat_duration"][(("result", "ok"),)][0] >= 1


def test_observe_turn_stream_cancelled():
    """流被取消（客户端断开 / idle 超时）→ result=cancelled，异常透传。"""

    async def gen():
        yield b"data: x\n\n"
        raise asyncio.CancelledError()

    class Resp:
        body_iterator = gen()

    resp = Resp()
    metrics.observe_turn(resp, time.monotonic())

    async def consume():
        with pytest.raises(asyncio.CancelledError):
            async for _ in resp.body_iterator:
                pass

    asyncio.run(consume())
    snap = _snapshot()
    assert snap["testpilot.copilot.chat_turns"].get((("result", "cancelled"),), 0) >= 1


def test_duration_buckets_second_scale():
    """回归：两个时长直方图桶边界为秒刻度定制——SDK 默认桶 (0,5,10,…,10000)
    是毫秒设计，工具调用普遍 0.01-5s 会全挤进 le=5 桶，分布不可用。"""
    metrics.TOOL_DURATION.record(0.05)
    metrics.CHAT_DURATION.record(2.0, {"result": "ok"})
    data = _reader.get_metrics_data()
    bounds: dict[str, list] = {}
    for rm in data.resource_metrics:
        for sm in rm.scope_metrics:
            for m in sm.metrics:
                for p in m.data.data_points:
                    if m.name in ("testpilot.copilot.tool_duration",
                                  "testpilot.copilot.chat_duration"):
                        bounds[m.name] = list(p.explicit_bounds)
    assert bounds["testpilot.copilot.tool_duration"] == [
        0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10]
    assert bounds["testpilot.copilot.chat_duration"] == [
        1, 5, 10, 30, 60, 120, 300, 600, 1800]


def test_chat_unhandled_exception_records_metric(monkeypatch):
    """回归：_chat_inner 未捕获异常（FastAPI 兜 500）→ chat_turns{result=error}
    必须记录且不进入流式路径——否则此类故障在指标上完全隐形。"""
    from testpilot_copilot import main

    class FakeReq:
        headers = {}

    async def boom(request):
        raise RuntimeError("boom")

    monkeypatch.setattr(main, "_chat_inner", boom)
    with pytest.raises(RuntimeError):
        asyncio.run(main.chat(FakeReq()))
    snap = _snapshot()
    assert snap["testpilot.copilot.chat_turns"].get((("result", "error"),), 0) >= 1
    assert snap["testpilot.copilot.active_streams"][()] == 0


def test_end_with_error_ends_span():
    """回归：end_with_error 记录异常、置错误状态并 end（修复 span 泄漏）。"""
    from opentelemetry.sdk.trace import TracerProvider
    from opentelemetry.sdk.trace.export import SpanExporter, SpanExportResult, SimpleSpanProcessor

    from testpilot_copilot import tracing

    class _Capture(SpanExporter):
        def __init__(self):
            self.finished: list = []

        def export(self, spans):
            self.finished.extend(spans)
            return SpanExportResult.SUCCESS

    cap = _Capture()
    tp = TracerProvider()
    tp.add_span_processor(SimpleSpanProcessor(cap))
    span = tp.get_tracer("t").start_span("x")
    tracing.end_with_error(span, ValueError("boom"))
    assert len(cap.finished) == 1
    assert cap.finished[0].status.status_code.name == "ERROR"
    assert any(e.name == "exception" for e in cap.finished[0].events)
