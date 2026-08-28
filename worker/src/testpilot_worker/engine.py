"""声明式执行引擎：递归执行 TestStep 树，产出 CaseResult + StepResult。"""

from __future__ import annotations

import asyncio
import logging
import time
from datetime import timedelta
from typing import Any, Awaitable, Callable, Mapping

import httpx
from google.protobuf import struct_pb2

from testpilot.common.v1 import types_pb2 as pb
from testpilot.worker.v1 import worker_pb2 as wpb

from . import grpc_exec, http_exec, lowcode_api, ui
from .assertions import evaluate
from .expr import ExprError, eval_expr, render
from .sandbox import ExecutionBackend, SubprocessBackend, bridge_http_handler

# 并行循环最大并发（P0 资源上限：每个迭代一个 httpx client，UI 步骤各起一个浏览器）
_MAX_PARALLEL_LOOP = 16          # 并行迭代并发上限
_MAX_LOOP_PARALLEL_TOTAL = 1000  # 并行迭代总量上限（gather 会先物化全部协程，巨量 count 直接 OOM）

_SDK_OP_MAP = {
    "eq": pb.ASSERTION_OP_EQ, "ne": pb.ASSERTION_OP_NE, "exists": pb.ASSERTION_OP_EXISTS,
    "contains": pb.ASSERTION_OP_CONTAINS, "matches": pb.ASSERTION_OP_MATCHES,
    "gt": pb.ASSERTION_OP_GT, "lt": pb.ASSERTION_OP_LT,
    "ge": pb.ASSERTION_OP_GE, "le": pb.ASSERTION_OP_LE,
    "type_is": pb.ASSERTION_OP_TYPE_IS,
}


def _sdk_assertions(records: list[dict]) -> list[pb.AssertionResult]:
    """SDK assert_that 记录 → AssertionResult proto。"""
    out = []
    for r in records:
        out.append(pb.AssertionResult(
            assertion=pb.Assertion(
                target=pb.ASSERTION_TARGET_CUSTOM,
                path=str(r.get("label", "")),
                op=_SDK_OP_MAP.get(r.get("op"), pb.ASSERTION_OP_EQ),
                expected=str(r.get("expected", "")),
            ),
            passed=bool(r.get("passed")),
            actual=str(r.get("actual", ""))[:2000],
            message=str(r.get("message", ""))[:2000],
        ))
    return out

OnProgress = Callable[[str, int, dict[str, Any]], Awaitable[None]]  # (step_path, status, detail)


def _to_struct(d: Mapping[str, Any]) -> struct_pb2.Struct:
    s = struct_pb2.Struct()
    s.update(dict(d))
    return s


class StepFailure(Exception):
    """步骤失败（fail-fast 中断用例）。"""


class CaseRunner:
    def __init__(self, task: wpb.TaskAssignment, on_progress: OnProgress | None = None,
                 case_rel_suffix: str = ""):
        self.task = task
        # 并行迭代产物隔离：同 run/case 的 UI 产物目录加迭代后缀（否则截图/
        # trace/har 互相覆盖——M4）
        self._case_rel_suffix = case_rel_suffix
        self.env = task.env
        self.on_progress = on_progress
        self.vars: dict[str, Any] = {}
        self.skipped_secrets: list[str] = []
        # 项目/环境级 HEADER 类变量：自动注入每个 api_call 请求头（接口显式配置同名头优先）
        self.auto_headers: dict[str, str] = {}
        for v in self.env.variables:
            if v.sensitive:
                self.skipped_secrets.append(v.key)
                continue
            self.vars[v.key] = v.value
            if v.category == pb.VARIABLE_CATEGORY_HEADER:
                self.auto_headers[v.key] = v.value
        self.base_url = self.env.base_url or self.env.environment.base_url
        self.last_response: dict[str, Any] | None = None
        self.step_results: list[pb.TestStepResult] = []
        self.client = httpx.AsyncClient(verify=True, transport=http_exec.pinned_transport())
        # tls_verify=false 的接口需要独立 insecure client（httpx 不支持单请求级 verify）
        self.insecure_client = httpx.AsyncClient(verify=False, transport=http_exec.pinned_transport())
        self._backend: ExecutionBackend | None = None
        self._ui: ui.UiSession | None = None
        self._last_ui_sr: pb.TestStepResult | None = None

    @property
    def backend(self) -> ExecutionBackend:
        if self._backend is None:
            # M9 修复：code_block / pre/post 脚本与低代码后端能力对齐——
            # 能力桥 HTTP 注入 HEADER 类环境变量，并支持 ui_action（Page 模型）。
            self._backend = SubprocessBackend(
                lambda args: bridge_http_handler(self.client, self.base_url, args, self.auto_headers),
                extra_ops={"ui_action": ui.bridge_ui_handler(lambda: self._ensure_ui())})
        return self._backend

    def _ensure_ui(self) -> ui.UiSession:
        """惰性创建本用例的 UiSession（声明式 UI 步骤与脚本 Page 模型共用）。"""
        if self._ui is None:
            case_rel = f"{self.task.run_id}/{self.task.functional.case_result_id}{self._case_rel_suffix}"
            self._ui = ui.UiSession(
                base_url=self.base_url,
                case_dir=ui.artifact_root() / case_rel,
                case_rel=case_rel,
                render=lambda s: render(s, self.scope()) if "{{" in s else s,
            )
        return self._ui

    def scope(self) -> dict[str, Any]:
        return {
            **self.vars,
            "vars": self.vars,
            "response": self.last_response,
            "base_url": self.base_url,
        }

    async def close(self):
        if self._ui is not None:
            # 导出 trace/har 产物并挂到最后一个 UI 步骤结果上
            arts = await self._ui.finish()
            if arts and self._last_ui_sr is not None:
                self._last_ui_sr.artifacts.extend(_artifact_refs(arts))
            self._ui = None
        await self.client.aclose()
        await self.insecure_client.aclose()

    # ---- 结果记录 ----

    def _record(self, path: str, status: int, elapsed_ms: int,
                request: dict | None = None, response: dict | None = None,
                assertions: list[pb.AssertionResult] | None = None,
                logs: list[str] | None = None,
                artifacts: list[ui.UiArtifact] | None = None,
                error: str | None = None):
        sr = pb.TestStepResult(
            case_result_id=self.task.functional.case_result_id,
            step_path=path,
            status=status,
        )
        sr.duration.FromTimedelta(timedelta(milliseconds=elapsed_ms))
        if request:
            sr.request.CopyFrom(_to_struct(request))
        if response:
            sr.response.CopyFrom(_to_struct(response))
        if assertions:
            sr.assertions.extend(assertions)
        if logs:
            sr.logs.extend(logs)
        if artifacts:
            sr.artifacts.extend(_artifact_refs(artifacts))
        if error:
            sr.error = error   # 步骤级失败原文（get_run include_steps 回传，探测/根因分析用）
        self.step_results.append(sr)

    async def _progress(self, path: str, status: int, detail: dict[str, Any] | None = None):
        if self.on_progress:
            await self.on_progress(path, status, detail or {})

    # ---- 主流程 ----

    async def run(self) -> tuple[int, str, int]:
        """执行用例，返回 (CaseStatus, error, duration_ms)。"""
        started = time.perf_counter()
        case = self.task.functional.case
        steps = case.declarative.steps if case.HasField("declarative") else []
        timeout_s = max(self.task.timeout.ToSeconds() or 300, 1)
        try:
            await asyncio.wait_for(self._run_steps(steps, ""), timeout=timeout_s)
            status, error = pb.CASE_STATUS_PASSED, ""
        except StepFailure as e:
            status, error = pb.CASE_STATUS_FAILED, str(e)
        except TimeoutError:
            status, error = pb.CASE_STATUS_FAILED, f"case timeout after {timeout_s}s"
        except Exception as e:  # 防御：未预期错误
            status, error = pb.CASE_STATUS_FAILED, f"engine error: {e}"
        elapsed = int((time.perf_counter() - started) * 1000)
        return status, error, elapsed

    async def _run_steps(self, steps, prefix: str):
        for idx, step in enumerate(steps, start=1):
            path = f"{prefix}{idx}"
            await self._run_step(step, path)

    # ---- 单步分发 ----

    async def _run_step(self, step: pb.TestStep, path: str):
        kind = step.WhichOneof("params")
        started = time.perf_counter()
        logs: list[str] = []
        await self._progress(path, pb.STEP_STATUS_RUNNING, {"name": step.name, "type": kind or ""})
        try:
            request, response, assertion_results, artifacts = None, None, None, None
            if kind == "api_call":
                request, response = await self._do_api_call(step.api_call, logs)
            elif kind == "assertion":
                assertion_results = self._do_assertions(step.assertion)
                if not all(r.passed for r in assertion_results):
                    failed = next(r for r in assertion_results if not r.passed)
                    raise StepFailure(
                        f"assertion failed at {path}: {failed.message}")
            elif kind == "code_block":
                assertion_results = await self._do_code_block(step.code_block, logs)
            elif kind == "ui_action":
                artifacts = await self._do_ui_action(step.ui_action, logs)
            elif kind == "set_var":
                self._do_set_var(step.set_var, logs)
            elif kind == "if_step":
                await self._do_if(step.if_step, path)
            elif kind == "loop_step":
                await self._do_loop(step.loop_step, path)
            elif kind == "retry_step":
                await self._do_retry(step.retry_step, path)
            elif kind == "delay":
                secs = step.delay.duration.ToTimedelta().total_seconds()
                await asyncio.sleep(max(secs, 0))
                logs.append(f"delayed {secs:g}s")
            elif kind == "grpc_call":
                request, response = await self._do_grpc_call(step.grpc_call, logs)
            else:
                raise StepFailure(f"step {path}: no params set")
            self._record(path, pb.STEP_STATUS_PASSED, _ms(started),
                         request=request, response=response,
                         assertions=assertion_results, logs=logs or None,
                         artifacts=artifacts)
            self._mark_last_ui(kind)
            await self._progress(path, pb.STEP_STATUS_PASSED)
        except StepFailure as e:
            fail_arts = await self._ui_failure_shot(kind)
            self._record(path, pb.STEP_STATUS_FAILED, _ms(started),
                         logs=[*logs, str(e)], artifacts=fail_arts or None,
                         error=str(e))
            self._mark_last_ui(kind)
            await self._progress(path, pb.STEP_STATUS_FAILED, {"error": str(e)})
            raise
        except Exception as e:
            fail_arts = await self._ui_failure_shot(kind)
            msg = f"{type(e).__name__}: {e}"
            self._record(path, pb.STEP_STATUS_FAILED, _ms(started),
                         logs=[*logs, msg],
                         artifacts=fail_arts or None,
                         error=msg)
            self._mark_last_ui(kind)
            await self._progress(path, pb.STEP_STATUS_FAILED, {"error": str(e)})
            raise StepFailure(f"step {path}: {e}") from e

    # ---- 各步骤实现 ----

    def _resolve_api(self, spec: pb.ApiCallStep) -> pb.HttpApi:
        if spec.HasField("inline"):
            api = pb.HttpApi()
            api.CopyFrom(spec.inline)
        elif spec.api_id:
            # api_id 由 Scheduler 派发前解析为 inline 快照；引擎直接见到裸引用属契约破坏。
            raise StepFailure(
                f"api_id reference ({spec.api_id}) must be resolved by scheduler; "
                "engine accepts inline only")
        else:
            raise StepFailure("api_call: neither api_id nor inline set")
        if spec.HasField("override"):
            ov = spec.override
            if ov.HasField("method"):
                api.method = ov.method
            if ov.HasField("uri"):
                api.uri = ov.uri
            if ov.HasField("body"):
                api.body.CopyFrom(ov.body)
            if ov.headers:
                merged = {kv.key: kv for kv in api.headers}
                for kv in ov.headers:
                    merged[kv.key] = kv
                del api.headers[:]
                api.headers.extend(merged.values())
            if ov.params:
                merged = {kv.key: kv for kv in api.params}
                for kv in ov.params:
                    merged[kv.key] = kv
                del api.params[:]
                api.params.extend(merged.values())
        return api

    def _inline_files(self) -> dict[str, bytes]:
        """binary_ref 负载：Scheduler 派发前从 artifact 解析进 task.functional.inline_files。"""
        return dict(self.task.functional.inline_files)

    def _client_for(self, api: pb.HttpApi) -> httpx.AsyncClient:
        """按 ApiSettings.tls_verify 选择 client；缺省=true（与历史行为一致）。"""
        if (api.HasField("settings") and api.settings.HasField("tls_verify")
                and not api.settings.tls_verify):
            return self.insecure_client
        return self.client

    def _script_payload(self, with_response: bool = False) -> dict[str, Any]:
        """pre/post 脚本与 code_block 共用的沙箱载荷。"""
        payload: dict[str, Any] = {
            "vars": {k: v for k, v in self.vars.items() if k != "response"},
            "base_url": self.base_url,
            "parameters": {},
            "tenant_id": self.task.tenant_id,
        }
        if with_response:
            payload["response"] = self.last_response
        return payload

    async def _run_script(self, script: pb.Script, phase: str, logs: list[str]) -> None:
        """执行一个接口级脚本片段（source 定义 run(ctx)，可 async）。

        语义与 code_block 一致：沙箱内零凭据、HTTP 经能力桥；res.vars 合并回用例
        上下文（pre 影响本次请求渲染，post 可影响后续步骤）。
        """
        if not script.source.strip():
            return
        if not script.enabled:
            return
        res = await self.backend.run(
            source=script.source,
            entry="run",
            payload=self._script_payload(with_response=phase == "post"),
            timeout_s=min(self.task.timeout.ToSeconds() or 120, 300),
        )
        logs.extend(res.logs[-50:])
        if res.vars:
            self.vars.update(res.vars)
            logs.append(f"{phase} script vars updated: {sorted(res.vars)}")
        if not res.ok:
            tail = res.error.strip().splitlines()
            raise StepFailure(f"{phase} script failed: {tail[-1] if tail else 'unknown'}")

    async def _run_scripts(self, scripts, phase: str, logs: list[str]) -> None:
        for sc in scripts:
            await self._run_script(sc, phase, logs)

    async def _do_api_call(self, spec: pb.ApiCallStep, logs: list[str]):
        api = self._resolve_api(spec)
        # 接口级前置脚本：先跑（可写入 ctx.vars），再渲染/发起请求
        await self._run_scripts(api.pre_scripts, "pre", logs)
        # 默认 auth 注入：HEADER 类环境变量并入（值可含 {{var}} 模板，http_exec 统一渲染；
        # 接口显式配置的同名头优先，忽略大小写）
        existing = {kv.key.lower() for kv in api.headers}
        for k, v in self.auto_headers.items():
            if k.lower() not in existing:
                api.headers.add(key=k, value=v)
        req_snap, resp_snap, resp_scope = await http_exec.execute(
            self._client_for(api), api, self.base_url, self.scope(), self._inline_files())
        self.last_response = resp_scope
        self.vars["response"] = resp_scope
        logs.append(f"{req_snap['method']} {req_snap['url']} -> {resp_snap['status']} ({resp_snap['elapsed_ms']}ms)")
        # 接口级后置脚本：拿到 response 后可断言/改写变量
        await self._run_scripts(api.post_scripts, "post", logs)
        return req_snap, resp_snap

    def _do_assertions(self, spec: pb.AssertionStep) -> list[pb.AssertionResult]:
        results = [evaluate(a, self.last_response, self.scope()) for a in spec.assertions]
        return results

    def _do_set_var(self, spec: pb.SetVarStep, logs: list[str]):
        if not spec.key:
            raise StepFailure("set_var: empty key")
        expr_src = spec.value_expr.strip()
        if "{{" in expr_src:
            value = render(expr_src, self.scope())
        else:
            try:
                value = eval_expr(expr_src, self.scope())
            except ExprError as e:
                raise StepFailure(f"set_var {spec.key}: {e}") from e
        self.vars[spec.key] = value
        logs.append(f"set {spec.key} = {value!r}")

    async def _do_code_block(self, spec: pb.CodeBlockStep, logs: list[str]) -> list[pb.AssertionResult]:
        if spec.lang and spec.lang != "python":
            raise StepFailure(f"code_block lang '{spec.lang}' not supported (python only)")
        res = await self.backend.run(
            source=spec.source,
            entry="run",
            payload={
                "vars": {k: v for k, v in self.vars.items() if k != "response"},
                "base_url": self.base_url,
                "parameters": {},
                "tenant_id": self.task.tenant_id,
            },
            timeout_s=min(self.task.timeout.ToSeconds() or 120, 300),
        )
        logs.extend(res.logs[-50:])
        if res.vars:
            self.vars.update(res.vars)
            logs.append(f"code_block vars updated: {sorted(res.vars)}")
        if not res.ok:
            tail = res.error.strip().splitlines()
            raise StepFailure(f"code_block failed: {tail[-1] if tail else 'unknown'}")
        return _sdk_assertions(res.assertions)

    async def _ui_failure_shot(self, kind: str | None) -> list[ui.UiArtifact]:
        """UI 步骤失败时的现场快照（尽力而为）。"""
        if kind != "ui_action" or self._ui is None:
            return []
        return await self._ui.failure_screenshot()

    def _mark_last_ui(self, kind: str | None):
        """把刚记录的步骤登记为 trace/har 产物挂载点。"""
        if kind == "ui_action" and self._ui is not None and self.step_results:
            self._last_ui_sr = self.step_results[-1]

    async def _do_ui_action(self, spec: pb.UiActionStep, logs: list[str]) -> list[ui.UiArtifact]:
        arts = await self._ensure_ui().execute(spec.action, spec.target, spec.value, logs)
        return arts

    async def _do_if(self, spec: pb.IfStep, path: str):
        try:
            cond = bool(eval_expr(spec.condition_expr, self.scope()))
        except ExprError as e:
            raise StepFailure(f"if condition: {e}") from e
        branch, steps = ("then", spec.then_steps) if cond else ("else", spec.else_steps)
        for idx, sub in enumerate(steps, start=1):
            await self._run_step(sub, f"{path}.{branch}.{idx}")

    async def _do_loop(self, spec: pb.LoopStep, path: str):
        bounds = spec.WhichOneof("bounds")
        if bounds == "count":
            rng = range(spec.count)
        elif bounds == "range":
            rng = range(spec.range.start, spec.range.end)
        else:
            raise StepFailure("loop: no bounds set")
        var = spec.iterator or "i"
        if spec.parallel:
            await self._do_loop_parallel(spec, rng, var, path)
            return
        # 迭代变量不泄漏：保存原值，循环结束恢复（防覆盖用户变量/后续步骤读到残留 int）
        had_var = var in self.vars
        saved_var = self.vars.get(var)
        try:
            for iteration, i in enumerate(rng, start=1):
                self.vars[var] = i
                for idx, sub in enumerate(spec.body_steps, start=1):
                    await self._run_step(sub, f"{path}.loop.{iteration}.{idx}")
        finally:
            if had_var:
                self.vars[var] = saved_var
            else:
                self.vars.pop(var, None)

    async def _do_loop_parallel(self, spec: pb.LoopStep, rng: range, var: str, path: str):
        """并行迭代：每个迭代在独立 CaseRunner 上执行 —— 变量取进入 loop 时的快照
        （隔离：迭代内 SET_VAR 不互相可见，也不写回父作用域），步骤结果按迭代顺序合并。
        语义：全部迭代跑完（不 fail-fast 取消）；任一迭代失败则该 LOOP 步骤失败，
        错误信息带迭代号。
        并发上限 _MAX_PARALLEL_LOOP：无上限时单用例可拉起上千浏览器/连接池（租户可触发 DoS）。
        总量上限 _MAX_LOOP_PARALLEL_TOTAL：asyncio.gather(*生成器) 会在信号量生效前
        物化全部迭代协程——10^6 级 count 直接 OOM Worker，必须限总量。"""
        if len(rng) > _MAX_LOOP_PARALLEL_TOTAL:
            raise StepFailure(
                f"loop parallel iterations {len(rng)} exceed limit {_MAX_LOOP_PARALLEL_TOTAL}")
        base_vars = dict(self.vars)
        base_response = self.last_response
        sem = asyncio.Semaphore(_MAX_PARALLEL_LOOP)

        async def one(i: int, iteration: int) -> list[pb.TestStepResult]:
            async with sem:
                # 产物目录按迭代隔离（M4）：并行迭代各自浏览器截图/trace 不互相覆盖
                clone = CaseRunner(self.task, self.on_progress,
                                   case_rel_suffix=f"-i{iteration}")
                clone.vars = dict(base_vars)
                clone.vars[var] = i
                clone.last_response = base_response
                try:
                    await clone._run_steps(spec.body_steps, f"{path}.loop.{iteration}.")
                finally:
                    await clone.close()
                return clone.step_results

        results = await asyncio.gather(
            *(one(i, n) for n, i in enumerate(rng, start=1)), return_exceptions=True)
        # 先合并全部成功迭代的结果，再统一抛第一个失败（不丢已完成迭代的产出）。
        first_failure: tuple[int, BaseException] | None = None
        for iteration, res in enumerate(results, start=1):
            if isinstance(res, BaseException):
                if first_failure is None:
                    first_failure = (iteration, res)
                continue
            self.step_results.extend(res)
        if first_failure is not None:
            iteration, res = first_failure
            if isinstance(res, StepFailure):
                raise StepFailure(f"loop parallel iteration {iteration} failed: {res}")
            raise res

    async def _do_grpc_call(self, spec: pb.GrpcCallStep, logs: list[str]):
        """GRPC_CALL：grpc_api_id 由 Scheduler 派发前解析进 task.grpc_apis；
        执行走 server reflection（见 grpc_exec），响应挂在 self.last_response
        （JSONPATH 断言对 `$.json.字段` 生效）。"""
        task = self.task.functional
        api = task.grpc_apis.get(spec.grpc_api_id)
        if api is None:
            raise StepFailure(
                f"grpc_api_id {spec.grpc_api_id!r} must be resolved by scheduler")
        target = grpc_exec.target_from_base_url(self.base_url)
        # 调用超时：任务级 timeout 与 30s 默认上限取小（防无 deadline 挂死线程池）
        task_timeout = self.task.timeout.ToSeconds() or 300
        grpc_timeout = min(max(task_timeout, 5), 30.0)
        try:
            request, response = await grpc_exec.call_async(
                target, api,
                request_override=dict(spec.request_override) if spec.HasField("request_override") else None,
                metadata_override=[(kv.key, kv.value) for kv in spec.metadata_override] or None,
                timeout_s=grpc_timeout)
        except grpc_exec.GrpcCallError as e:
            raise StepFailure(f"grpc_call: {e}") from e
        self.last_response = response
        logs.append(f"grpc {api.full_service}.{api.method} -> {target}")
        return request, response

    async def _do_retry(self, spec: pb.RetryStep, path: str):
        if not spec.HasField("body_step"):
            raise StepFailure("retry: no body_step")
        attempts = max(spec.max_attempts, 1)
        backoff = max(spec.backoff.ToTimedelta().total_seconds(), 0)
        last_err: Exception | None = None
        for attempt in range(1, attempts + 1):
            try:
                await self._run_step(spec.body_step, f"{path}.retry.{attempt}.1")
                return
            except StepFailure as e:
                last_err = e
                if attempt < attempts and backoff:
                    await asyncio.sleep(backoff)
        raise StepFailure(f"retry exhausted {attempts} attempts: {last_err}")


def _ms(started: float) -> int:
    return int((time.perf_counter() - started) * 1000)


def _artifact_refs(arts: list[ui.UiArtifact]) -> list[pb.ArtifactRef]:
    return [pb.ArtifactRef(kind=a.kind, uri=a.uri, size=a.size) for a in arts]


async def run_task(task: wpb.TaskAssignment,
                   on_progress: OnProgress | None = None) -> wpb.TaskResult:
    """执行一个 FunctionalTask，返回 TaskResult（含 case/step 结果）。"""
    started = time.perf_counter()
    case = task.functional.case

    if case.HasField("lowcode"):
        status, error, elapsed, step_results = await _run_lowcode(task)
    else:
        runner = CaseRunner(task, on_progress)
        try:
            status, error, elapsed = await runner.run()
        finally:
            await runner.close()
        step_results = runner.step_results

    result = wpb.TaskResult(task_id=task.task_id, run_id=task.run_id, error=error)
    result.status = pb.RUN_STATUS_PASSED if status == pb.CASE_STATUS_PASSED else pb.RUN_STATUS_FAILED
    result.duration.FromTimedelta(timedelta(milliseconds=int((time.perf_counter() - started) * 1000)))

    cr = pb.TestCaseResult(
        id=task.functional.case_result_id,
        run_id=task.run_id,
        case_id=case.id,
        status=status,
        error=error,
    )
    cr.duration.FromTimedelta(timedelta(milliseconds=elapsed))
    result.case_results.append(cr)
    result.step_results.extend(step_results)
    return result


async def _run_lowcode(task: wpb.TaskAssignment) -> tuple[int, str, int, list[pb.TestStepResult]]:
    """低代码用例：整脚本进沙箱，经能力桥执行副作用（HTTP/变量/UI/按 ID 调用）。"""
    case = task.functional.case
    lc = case.lowcode
    started = time.perf_counter()
    base_url = task.env.base_url or task.env.environment.base_url
    vars_init = {v.key: v.value for v in task.env.variables if not v.sensitive}
    auto_headers = {v.key: v.value for v in task.env.variables
                    if not v.sensitive and v.category == pb.VARIABLE_CATEGORY_HEADER}
    payload = {
        "vars": vars_init,
        "base_url": base_url,
        "parameters": lowcode_api.parameters_to_dict(lc),
        "tenant_id": task.tenant_id,
        "http_api_ids": sorted(task.functional.http_apis),
        "grpc_api_ids": sorted(task.functional.grpc_apis),
    }

    if lc.WhichOneof("script") != "source":
        # script_ref 由 Scheduler 派发前解析为内联 source；引擎直接见到裸 ref 属契约破坏。
        return (pb.CASE_STATUS_FAILED, "lowcode script_ref must be resolved by scheduler (engine accepts source only)", 0, [])

    # Page 模型：桥 op=ui_action 转发到本用例的 UiSession（惰性启动浏览器），
    # 产物（截图/trace/har）按 run/case 目录隔离，结束后挂到步骤结果。
    case_rel = f"{task.run_id}/{task.functional.case_result_id}"
    ui_session: ui.UiSession | None = None
    bridge_artifacts: list[ui.UiArtifact] = []
    ui_logs: list[str] = []

    def get_session() -> ui.UiSession:
        nonlocal ui_session
        if ui_session is None:
            ui_session = ui.UiSession(
                base_url=base_url,
                case_dir=ui.artifact_root() / case_rel,
                case_rel=case_rel,
                render=lambda s: s,  # 低代码脚本自行渲染，桥不处理模板
            )
        return ui_session

    async def handle_ui_action(args: dict) -> dict:
        """包装 ui_action 桥：与声明式 UI_ACTION 一致地采集步骤日志/截图产物，
        并在浏览器操作失败时立即保存现场截图（不掩盖原始错误）。"""
        try:
            out = await ui.bridge_ui_handler(get_session)(args)
        except Exception:
            try:
                bridge_artifacts.extend(await get_session().failure_screenshot())
            except Exception as e:  # 现场截图失败不能吞掉原始错误
                logging.getLogger(__name__).warning(
                    "lowcode ui failure screenshot failed: %s", e)
            raise
        ui_logs.extend(str(x) for x in out.get("logs") or [])
        for item in out.get("artifacts") or []:
            try:
                bridge_artifacts.append(ui.UiArtifact(
                    kind=str(item.get("kind") or "artifact"),
                    uri=str(item.get("uri") or ""),
                    size=int(item.get("size") or 0)))
            except (AttributeError, TypeError, ValueError):
                continue
        return out

    timeout = min(task.timeout.ToSeconds() or 120, 300)
    caller = lowcode_api.LowCodeApiCaller(
        base_url=base_url, tenant_id=task.tenant_id, auto_headers=auto_headers,
        http_apis=task.functional.http_apis, grpc_apis=task.functional.grpc_apis,
        inline_files=task.functional.inline_files,
        parameters=payload["parameters"], timeout_s=timeout)
    extra_files: dict[str, str] | None = None
    if task.functional.api_wrappers_source:
        extra_files = {"tp_api_wrappers.py": task.functional.api_wrappers_source}
    try:
        backend: ExecutionBackend = SubprocessBackend(
            lambda args: bridge_http_handler(caller.client, base_url, args, auto_headers),
            extra_ops={"ui_action": handle_ui_action},
            api_handler=caller.handle,
            grpc_handler=caller.raw_grpc)
        res = await backend.run(lc.source, lc.entry or "run", payload, timeout,
                                extra_files=extra_files)
    finally:
        await caller.close()
        if ui_session is not None:
            try:
                bridge_artifacts.extend(await ui_session.finish())
            except Exception as e:  # 防御：浏览器收尾失败不影响用例结果
                logging.getLogger(__name__).warning("ui session finish failed: %s", e)

    elapsed = int((time.perf_counter() - started) * 1000)
    status = pb.CASE_STATUS_PASSED if res.ok else pb.CASE_STATUS_FAILED
    error = "" if res.ok else (res.error.strip().splitlines()[-1] if res.error.strip() else "script failed")

    sr = pb.TestStepResult(
        case_result_id=task.functional.case_result_id,
        step_path="1",
        status=pb.STEP_STATUS_PASSED if res.ok else pb.STEP_STATUS_FAILED,
    )
    sr.duration.FromTimedelta(timedelta(milliseconds=res.duration_ms or elapsed))
    logs = list(res.logs or [])
    if caller.logs:
        logs = list(caller.logs[-200:]) + logs
    if ui_logs:
        logs.extend(ui_logs[-200:])
    if logs:
        sr.logs.extend(logs[-500:])
    sr.assertions.extend(_sdk_assertions(res.assertions))
    if bridge_artifacts:
        sr.artifacts.extend(_artifact_refs(bridge_artifacts))
    return status, error, elapsed, [sr]
