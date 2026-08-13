"""engine.py：无网纯逻辑 —— SDK 断言记录转换、API 解析/覆盖合并、
变量初始化与 set_var、scope 合并、retry/loop 入参校验。"""

import asyncio

import pytest
from testpilot.common.v1 import types_pb2 as pb
from testpilot.worker.v1 import worker_pb2 as wpb

from testpilot_worker.engine import (
    CaseRunner,
    StepFailure,
    _sdk_assertions,
    _to_struct,
)


def _runner(**env_kwargs) -> CaseRunner:
    task = wpb.TaskAssignment()
    for k, v in env_kwargs.items():
        setattr(task.env, k, v)
    return CaseRunner(task)


# ---- _sdk_assertions ----

def test_sdk_assertions_mapping():
    recs = [
        {"label": "status", "op": "eq", "actual": 200, "expected": 200,
         "passed": True, "message": "eq 200"},
        {"label": "body", "op": "contains", "actual": "hello", "expected": "hell",
         "passed": False, "message": "contains 'hell'"},
    ]
    out = _sdk_assertions(recs)
    assert len(out) == 2
    assert out[0].assertion.target == pb.ASSERTION_TARGET_CUSTOM
    assert out[0].assertion.path == "status"
    assert out[0].assertion.op == pb.ASSERTION_OP_EQ
    assert out[0].passed is True
    assert out[1].assertion.op == pb.ASSERTION_OP_CONTAINS
    assert out[1].passed is False
    assert out[1].message == "contains 'hell'"


def test_sdk_assertions_unknown_op_falls_back_to_eq():
    out = _sdk_assertions([{"op": "bogus"}])
    assert out[0].assertion.op == pb.ASSERTION_OP_EQ


def test_sdk_assertions_truncates_long_fields():
    out = _sdk_assertions([{"op": "eq", "actual": "x" * 5000, "message": "m" * 5000}])
    assert len(out[0].actual) == 2000
    assert len(out[0].message) == 2000


def test_to_struct_roundtrip():
    s = _to_struct({"a": 1, "b": "x", "c": [1, 2]})
    assert dict(s) == {"a": 1.0, "b": "x", "c": [1.0, 2.0]}  # Struct 数字均为 double


# ---- CaseRunner 初始化：变量提取 ----

def test_init_skips_sensitive_vars():
    task = wpb.TaskAssignment()
    task.env.variables.add(key="plain", value="v1")
    task.env.variables.add(key="secret", value="s3cr3t", sensitive=True)
    r = CaseRunner(task)
    assert r.vars == {"plain": "v1"}
    assert r.skipped_secrets == ["secret"]


def test_init_base_url_fallback():
    task = wpb.TaskAssignment()
    task.env.environment.base_url = "http://from-env-object"
    assert CaseRunner(task).base_url == "http://from-env-object"
    task.env.base_url = "http://explicit"
    assert CaseRunner(task).base_url == "http://explicit"  # env.base_url 优先


def test_scope_merges_roots():
    r = _runner(base_url="http://b")
    r.vars["token"] = "t"
    r.last_response = {"status": 200}
    scope = r.scope()
    assert scope["token"] == "t"               # 变量平铺
    assert scope["vars"] == {"token": "t"}     # vars 根
    assert scope["response"] == {"status": 200}
    assert scope["base_url"] == "http://b"


# ---- _do_set_var ----

def test_set_var_plain_expr():
    r = _runner()
    logs = []
    r._do_set_var(pb.SetVarStep(key="x", value_expr="1 + 2"), logs)
    assert r.vars["x"] == 3
    assert "x" in logs[0]


def test_set_var_template_preserves_native_type():
    r = _runner()
    r.vars["items"] = [1, 2]
    r._do_set_var(pb.SetVarStep(key="dup", value_expr="{{ items }}"), [])
    assert r.vars["dup"] == [1, 2]
    r._do_set_var(pb.SetVarStep(key="s", value_expr="n={{ items[0] }}"), [])
    assert r.vars["s"] == "n=1"


def test_set_var_empty_key_fails():
    r = _runner()
    with pytest.raises(StepFailure, match="empty key"):
        r._do_set_var(pb.SetVarStep(key="", value_expr="1"), [])


def test_set_var_bad_expr_fails():
    r = _runner()
    with pytest.raises(StepFailure, match="undefined name"):
        r._do_set_var(pb.SetVarStep(key="x", value_expr="ghost + 1"), [])


# ---- _resolve_api ----

def _api_call(**kwargs) -> pb.ApiCallStep:
    spec = pb.ApiCallStep()
    inline = kwargs.pop("inline", None)
    if inline is not None:
        spec.inline.CopyFrom(inline)
    for k, v in kwargs.items():
        setattr(spec, k, v)
    return spec


def test_resolve_api_inline_copy():
    inline = pb.HttpApi(method=pb.HTTP_METHOD_GET, uri="/users")
    r = _runner()
    api = r._resolve_api(_api_call(inline=inline))
    assert api.method == pb.HTTP_METHOD_GET
    assert api.uri == "/users"
    api.uri = "/mutated"
    assert inline.uri == "/users"  # 返回副本，不回写 spec


def test_resolve_api_api_id_unsupported():
    r = _runner()
    with pytest.raises(StepFailure, match="api_id reference"):
        r._resolve_api(_api_call(api_id="api-123"))


def test_resolve_api_neither_set():
    r = _runner()
    with pytest.raises(StepFailure, match="neither api_id nor inline"):
        r._resolve_api(pb.ApiCallStep())


def test_resolve_api_override_merges_headers_and_scalars():
    inline = pb.HttpApi(method=pb.HTTP_METHOD_GET, uri="/old")
    inline.headers.add(key="A", value="1")
    inline.headers.add(key="B", value="2")
    spec = _api_call(inline=inline)
    spec.override.method = pb.HTTP_METHOD_POST
    spec.override.uri = "/new"
    spec.override.headers.add(key="B", value="2-override")
    spec.override.headers.add(key="C", value="3")
    r = _runner()
    api = r._resolve_api(spec)
    assert api.method == pb.HTTP_METHOD_POST
    assert api.uri == "/new"
    headers = {kv.key: kv.value for kv in api.headers}
    assert headers == {"A": "1", "B": "2-override", "C": "3"}


# ---- retry / loop 入参校验（async 但无 IO）----

def test_retry_without_body_fails():
    r = _runner()
    with pytest.raises(StepFailure, match="no body_step"):
        asyncio.run(r._do_retry(pb.RetryStep(), "1"))


def test_loop_without_bounds_fails():
    r = _runner()
    with pytest.raises(StepFailure, match="no bounds set"):
        asyncio.run(r._do_loop(pb.LoopStep(), "1"))


# ---- loop parallel ----

def _set_var_step(key: str, value_expr: str) -> pb.TestStep:
    return pb.TestStep(name=f"set {key}",
                       set_var=pb.SetVarStep(key=key, value_expr=value_expr))


def _delay_step(secs: float) -> pb.TestStep:
    step = pb.TestStep(name="delay")
    step.delay.duration.FromMilliseconds(int(secs * 1000))
    return step


def test_loop_parallel_var_isolation():
    r = _runner()
    r.vars["base"] = 10
    spec = pb.LoopStep(iterator="i", count=3, parallel=True,
                       body_steps=[_set_var_step("local", "{{ i }} * 2")])
    asyncio.run(r._do_loop(spec, "1"))
    # 父作用域不被迭代写入（仅快照读取）；结果按迭代顺序合并
    assert "local" not in r.vars
    assert r.vars["base"] == 10
    paths = [sr.step_path for sr in r.step_results]
    assert paths == ["1.loop.1.1", "1.loop.2.1", "1.loop.3.1"]
    assert all(sr.status == pb.STEP_STATUS_PASSED for sr in r.step_results)


def test_loop_parallel_uses_iterator_snapshot():
    # 每个迭代看到的 vars 是进入 loop 时的快照 + iterator 变量（不串扰）
    r = _runner()
    spec = pb.LoopStep(iterator="i", count=2, parallel=True,
                       body_steps=[_set_var_step("seen", "{{ i }} + 100")])
    asyncio.run(r._do_loop(spec, "1"))
    assert "seen" not in r.vars  # 写入被隔离
    assert len(r.step_results) == 2


def test_loop_parallel_failure_reports_iteration():
    r = _runner()
    bad = pb.TestStep(name="bad")  # 无 params → StepFailure
    only_iter2 = pb.TestStep(
        name="guard",
        if_step=pb.IfStep(condition_expr="i == 1", then_steps=[bad], else_steps=[]))
    spec = pb.LoopStep(iterator="i", count=3, parallel=True,
                       body_steps=[_set_var_step("ok", "1"), only_iter2])
    with pytest.raises(StepFailure, match="iteration 2"):
        asyncio.run(r._do_loop(spec, "1"))


def test_loop_parallel_all_iterations_complete_before_failure():
    # 不 fail-fast：失败在 gather 结束后统一抛出，其余迭代的步骤结果仍被保留。
    r = _runner()
    bad = pb.TestStep(name="bad")
    only_iter2 = pb.TestStep(
        name="guard",
        if_step=pb.IfStep(condition_expr="i == 1", then_steps=[bad], else_steps=[]))
    spec = pb.LoopStep(iterator="i", count=3, parallel=True,
                       body_steps=[_set_var_step("ok", "1"), only_iter2])
    with pytest.raises(StepFailure):
        asyncio.run(r._do_loop(spec, "1"))
    # 迭代 1、3 的 set_var 结果仍在（结果已合并到 self.step_results）
    ok_paths = [sr.step_path for sr in r.step_results]
    assert "1.loop.1.1" in ok_paths
    assert "1.loop.3.1" in ok_paths


def test_loop_parallel_runs_concurrently():
    import time
    r = _runner()
    spec = pb.LoopStep(iterator="i", count=3, parallel=True,
                       body_steps=[_delay_step(0.15)])
    started = time.perf_counter()
    asyncio.run(r._do_loop(spec, "1"))
    elapsed = time.perf_counter() - started
    assert elapsed < 0.35, f"parallel loop too slow: {elapsed:.2f}s (serial ≈0.45s)"


def test_loop_parallel_no_bounds_fails():
    r = _runner()
    spec = pb.LoopStep(parallel=True)
    with pytest.raises(StepFailure, match="no bounds"):
        asyncio.run(r._do_loop(spec, "1"))
