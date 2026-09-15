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
    assert len(tools.readonly.tools) == 17
    assert len(tools.writes.tools) == 21
    assert len(tools.probe.tools) == 6
    # 包装器不得破坏签名解析（__future__ annotations 的字符串求值路径）
    props = tools.writes.tools["update_api"].function_schema.json_schema["properties"]
    assert set(props) == {"api_id", "api", "kind"}


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
