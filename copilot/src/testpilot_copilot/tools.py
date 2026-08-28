"""Copilot 工具集：只读（免审批）+ 写/触发（requires_approval → HITL）。

工具经 Scheduler gRPC 执行，租户/用户上下文来自每次请求的 deps。
"""

from __future__ import annotations

import asyncio
import json
import logging
import os
import re
from dataclasses import dataclass
from typing import Any

import httpx
from pydantic_ai import RunContext
from pydantic_ai.toolsets import FunctionToolset

from testpilot.common.v1 import types_pb2 as pb
from testpilot.copilot.v1 import copilot_pb2 as cpb

from .scheduler_client import SchedulerClient, parse_struct, to_dict_async

log = logging.getLogger("testpilot.copilot")

# 敏感头掩码：租户可能把 Authorization/Cookie 等存在接口定义头里，
# 原样返回会把凭据送进外部 LLM 供应商上下文并落库（P2）
_SENSITIVE_HEADER_RE = re.compile(
    r"^(authorization|proxy-authorization|cookie|set-cookie|x-api-key|"
    r"x-auth-token|api[-_]?key|token)$", re.I)


def _redact_headers(headers: Any) -> None:
    """就地掩码敏感 header 的 value（key 保留便于 LLM 理解结构）。"""
    if not isinstance(headers, list):
        return
    for h in headers:
        if isinstance(h, dict) and _SENSITIVE_HEADER_RE.match(str(h.get("key", ""))):
            h["value"] = "***"


def _redact_cookies(cookies: Any) -> None:
    """cookie 值一律掩码：接口定义中的会话凭据不得进入外部 LLM 上下文。"""
    if not isinstance(cookies, list):
        return
    for c in cookies:
        if isinstance(c, dict):
            c["value"] = "***"


def _contains_redacted(values: Any) -> bool:
    """检测从 get_api 原样带回的掩码值：更新时不允许把 *** 写回接口定义。"""
    if not isinstance(values, list):
        return False
    return any(isinstance(v, dict) and str(v.get("value")) == "***" for v in values)


@dataclass
class CopilotDeps:
    sched: SchedulerClient
    tenant_id: int
    user_id: str
    http: httpx.AsyncClient      # scheduler REST（会话持久化）
    token: str
    # 前端页面左上角当前选择的项目/环境（随每次 chat 请求的 X-TP-Project-Id /
    # X-TP-Env-Id 头传入）。仅作为工具参数缺省值，最终访问控制仍由 Scheduler
    # 按 deps 中的租户/用户身份校验，ID 不参与任何信任边界。
    ui_project_id: str = ""
    ui_env_id: str = ""
    # hydrate_ui_context() 校验通过后的权威详情（Scheduler REST 返回）。
    ui_project: dict[str, Any] | None = None
    ui_environment: dict[str, Any] | None = None
    # UI 探测会话 ID（v1）：main.py 按 chat 会话生成注入，工具不再让 LLM 传会话 ID，
    # 杜绝串会话；Scheduler 侧按 tenant 归属校验。
    probe_session_id: str = ""

    def ctx(self) -> pb.RequestContext:
        return SchedulerClient.ctx(self.tenant_id, self.user_id)

    async def hydrate_ui_context(self) -> None:
        """用 Scheduler REST 校验页面上下文并加载权威详情。

        校验失败（项目不存在/环境不属于该项目/网络错误）时清空对应 ID 与详情，
        工具缺省参数回退为“未选择”，避免拿失效 ID 反复调用 gRPC。
        """
        self.ui_project = None
        self.ui_environment = None
        if self.http is None or not self.token:
            return
        h = {"Authorization": f"Bearer {self.token}"}

        async def load_project() -> None:
            try:
                r = await self.http.get(f"/api/v1/projects/{self.ui_project_id}", headers=h)
                if r.status_code == 200:
                    self.ui_project = r.json()
                else:
                    log.warning("copilot ui project %s not found: HTTP %s",
                                self.ui_project_id, r.status_code)
            except (httpx.HTTPError, ValueError) as e:
                log.warning("hydrate copilot ui project failed: %s", e)

        async def load_environment() -> None:
            try:
                r = await self.http.get(
                    f"/api/v1/environments?project_id={self.ui_project_id}&page_size=200",
                    headers=h)
                if r.status_code == 200:
                    items = r.json().get("items", [])
                    self.ui_environment = next(
                        (e for e in items if str(e.get("id")) == self.ui_env_id), None)
                if self.ui_environment is None:
                    log.warning("copilot ui environment %s not found in project %s",
                                self.ui_env_id, self.ui_project_id)
            except (httpx.HTTPError, ValueError) as e:
                log.warning("hydrate copilot ui environment failed: %s", e)

        # 项目详情与环境列表互不依赖：并行拉取，每个 chat 请求少一个串行 RTT
        if self.ui_project_id:
            if self.ui_env_id:
                await asyncio.gather(load_project(), load_environment())
            else:
                await load_project()
        if self.ui_project is None:
            self.ui_project_id = ""
        if self.ui_environment is None:
            self.ui_env_id = ""

    def resolve_project_id(self, project_id: str | None = None) -> str:
        """显式 project_id 优先；否则回退页面当前选择；都没有则给出可操作错误。"""
        pid = (project_id or "").strip()
        if not pid:
            pid = self.ui_project_id.strip()
        if not pid:
            raise ValueError(
                "未提供 project_id，且页面左上角当前未选择项目；"
                "请先在页面选择项目，或显式给出 project_id")
        return pid

    def resolve_env_id(self, env_id: str | None = None,
                       required: bool = False) -> str:
        """显式 env_id 优先；否则回退页面当前选择。required=False 时允许为空。"""
        eid = (env_id or "").strip()
        if not eid:
            eid = self.ui_env_id.strip()
        if not eid and required:
            raise ValueError(
                "未提供 env_id，且页面左上角当前未选择环境；"
                "请先在页面选择环境，或显式给出 env_id")
        return eid


# ---------------------------------------------------------------------------
# 只读工具（免审批）
# ---------------------------------------------------------------------------

readonly = FunctionToolset()


@readonly.tool
async def list_projects(ctx: RunContext[CopilotDeps], query: str = "") -> list[dict]:
    """列出当前租户的项目。"""
    r = await ctx.deps.sched.stub.ListProjects(
        cpb.ListProjectsRequest(ctx=ctx.deps.ctx(), query=query,
                                page=pb.PageRequest(page_size=100)))
    return (await to_dict_async(r)).get("projects", [])


def _project_context(data: dict | None) -> dict | None:
    """REST 项目 JSON → 与 gRPC 工具一致的 camelCase 摘要。"""
    if not data:
        return None
    return {
        "id": data.get("id"),
        "tenantId": data.get("tenant_id"),
        "name": data.get("name"),
        "description": data.get("description"),
        "createdAt": data.get("created_at"),
        "updatedAt": data.get("updated_at"),
    }


def _environment_context(data: dict | None) -> dict | None:
    """REST 环境 JSON → 与 gRPC 工具一致的 camelCase 摘要。"""
    if not data:
        return None
    return {
        "id": data.get("id"),
        "projectId": data.get("project_id"),
        "name": data.get("name"),
        "icon": data.get("icon"),
        "description": data.get("description"),
        "baseUrl": data.get("base_url"),
    }


@readonly.tool
async def get_current_context(ctx: RunContext[CopilotDeps]) -> dict:
    """获取用户当前在页面左上角选择的项目/环境（权威详情，含 id/name/baseUrl）。
    回答「当前项目/当前环境/这里有哪些接口」等问题前先调用本工具；
    其余工具的 project_id/environment_id 参数省略时也默认使用该选择。
    返回 project_selected/environment_selected 标志，未选择时不要臆造 ID。"""
    deps = ctx.deps
    # 正常 chat 入口已在 _chat_inner 校验并加载详情；直接构造 deps 的调用补一次。
    if deps.ui_project_id and deps.ui_project is None and deps.http is not None:
        await deps.hydrate_ui_context()
    if not deps.ui_project_id or deps.ui_project is None:
        return {
            "project_selected": False, "project": None,
            "environment_selected": False, "environment": None,
            "hint": "页面左上角未选择项目（或所选项目已失效）："
                    "请提醒用户重新选择项目，或调用 list_projects 让用户指定。",
        }

    env_hint = (
        "当前环境已选择，环境相关工具的缺省参数会使用它。"
        if deps.ui_environment is not None else
        "页面左上角未选择环境；环境相关操作请先用 list_environments 确定 env_id。"
    )
    return {
        "project_selected": True,
        "project": _project_context(deps.ui_project),
        "environment_selected": deps.ui_environment is not None,
        "environment": _environment_context(deps.ui_environment),
        "hint": env_hint,
    }


@readonly.tool
async def list_apis(ctx: RunContext[CopilotDeps], project_id: str | None = None,
                    query: str = "") -> list[dict]:
    """列出项目下的 HTTP 接口（id/method/uri/headers/params/body）。
    project_id 省略时使用页面左上角当前选择的项目。"""
    pid = ctx.deps.resolve_project_id(project_id)
    r = await ctx.deps.sched.stub.ListApis(
        cpb.ListApisRequest(ctx=ctx.deps.ctx(), project_id=pid, query=query,
                            page=pb.PageRequest(page_size=200)))
    apis = (await to_dict_async(r)).get("httpApis", [])
    for a in apis:
        _redact_headers(a.get("headers"))
        _redact_cookies(a.get("cookies"))
    return apis


@readonly.tool
async def get_api(ctx: RunContext[CopilotDeps], api_id: str) -> dict:
    """获取单个接口详情。"""
    r = await ctx.deps.sched.stub.GetApi(
        cpb.GetApiRequest(ctx=ctx.deps.ctx(), api_id=api_id, kind=cpb.API_KIND_HTTP))
    d = await to_dict_async(r)
    _redact_headers(d.get("http", {}).get("headers"))
    _redact_cookies(d.get("http", {}).get("cookies"))
    return d


@readonly.tool
async def list_environments(ctx: RunContext[CopilotDeps],
                            project_id: str | None = None) -> list[dict]:
    """列出项目环境（含 base_url）。project_id 省略时使用页面左上角当前选择的项目。"""
    pid = ctx.deps.resolve_project_id(project_id)
    r = await ctx.deps.sched.stub.ListEnvironments(
        cpb.ListEnvironmentsRequest(ctx=ctx.deps.ctx(), project_id=pid))
    return (await to_dict_async(r)).get("environments", [])


@readonly.tool
async def list_test_cases(ctx: RunContext[CopilotDeps],
                          project_id: str | None = None, query: str = "") -> list[dict]:
    """列出测试用例（声明式/低代码）。project_id 省略时使用页面左上角当前选择的项目。"""
    pid = ctx.deps.resolve_project_id(project_id)
    r = await ctx.deps.sched.stub.ListTestCases(
        cpb.ListTestCasesRequest(ctx=ctx.deps.ctx(), project_id=pid, query=query,
                                 page=pb.PageRequest(page_size=200)))
    return (await to_dict_async(r)).get("cases", [])


@readonly.tool
async def get_test_case(ctx: RunContext[CopilotDeps], case_id: str) -> dict:
    """获取用例完整定义（步骤树/低代码脚本）。"""
    r = await ctx.deps.sched.stub.GetTestCase(
        cpb.GetTestCaseRequest(ctx=ctx.deps.ctx(), case_id=case_id))
    return await to_dict_async(r)


@readonly.tool
async def query_schema(ctx: RunContext[CopilotDeps], topic: str = "") -> dict:
    """查询领域 schema（数据字典：实体/字段/枚举），写用例前先查。"""
    r = await ctx.deps.sched.stub.QuerySchema(
        cpb.QuerySchemaRequest(ctx=ctx.deps.ctx(), topic=topic))
    return await to_dict_async(r)


@readonly.tool
async def list_runs(ctx: RunContext[CopilotDeps], project_id: str | None = None,
                    plan_id: str = "", status: str = "") -> list[dict]:
    """列出测试运行记录。project_id 省略时默认页面左上角当前选择的项目；
    显式传空字符串表示不限项目。status 可选：RUN_STATUS_PASSED/FAILED/RUNNING 等。"""
    pid = ctx.deps.ui_project_id if project_id is None else project_id
    st = pb.RunStatus.Value(status) if status else pb.RUN_STATUS_UNSPECIFIED
    r = await ctx.deps.sched.stub.ListRuns(
        cpb.ListRunsRequest(ctx=ctx.deps.ctx(), project_id=pid, plan_id=plan_id,
                            status=st, page=pb.PageRequest(page_size=50)))
    return (await to_dict_async(r)).get("runs", [])


@readonly.tool
async def get_run(ctx: RunContext[CopilotDeps], run_id: str, include_steps: bool = False) -> dict:
    """获取运行详情（含用例结果；include_steps=true 含步骤级明细，每步带 error 原文，用于失败根因分析）。"""
    r = await ctx.deps.sched.stub.GetRun(
        cpb.GetRunRequest(ctx=ctx.deps.ctx(), run_id=run_id, include_steps=include_steps))
    return await to_dict_async(r)


@readonly.tool
async def query_coverage(ctx: RunContext[CopilotDeps],
                         project_id: str | None = None) -> dict:
    """接口 vs 用例覆盖率分析（未覆盖接口清单）。
    project_id 省略时使用页面左上角当前选择的项目。"""
    pid = ctx.deps.resolve_project_id(project_id)
    r = await ctx.deps.sched.stub.QueryCoverage(
        cpb.QueryCoverageRequest(ctx=ctx.deps.ctx(), project_id=pid))
    return await to_dict_async(r)


@readonly.tool
async def query_api_directory(ctx: RunContext[CopilotDeps],
                              project_id: str | None = None,
                              query: str = "", parent_node_id: str = "") -> dict:
    """查询接口目录树（目录/HTTP/gRPC 接口条目，含人读路径）。
    用于回答“某目录下有哪些接口 / 接口挂在哪”等问题；query 可按名称/uri 过滤。
    project_id 省略时使用页面左上角当前选择的项目。"""
    pid = ctx.deps.resolve_project_id(project_id)
    r = await ctx.deps.sched.stub.QueryApiDirectory(
        cpb.QueryApiDirectoryRequest(ctx=ctx.deps.ctx(), project_id=pid,
                                     query=query, parent_node_id=parent_node_id))
    return await to_dict_async(r)


@readonly.tool
async def check_variable_refs(ctx: RunContext[CopilotDeps],
                              project_id: str | None = None,
                              environment_id: str | None = None) -> dict:
    """检查项目接口/用例中的 {{expr}} 模板引用的变量是否都已定义。
    返回 defined_variables 与未定义根变量 issues；生成接口/用例后可用它自查。
    project_id/environment_id 省略时使用页面左上角当前选择（环境未选择则校验项目全部变量）。"""
    pid = ctx.deps.resolve_project_id(project_id)
    eid = ctx.deps.resolve_env_id(environment_id, required=False)
    r = await ctx.deps.sched.stub.CheckVariableRefs(
        cpb.CheckVariableRefsRequest(ctx=ctx.deps.ctx(), project_id=pid,
                                     environment_id=eid))
    return await to_dict_async(r)


# ---------------------------------------------------------------------------
# 写/触发工具（requires_approval → 前端 HITL 审批后执行，Scheduler 落审计）
# ---------------------------------------------------------------------------

writes = FunctionToolset()


@writes.tool(requires_approval=True)
async def create_project(ctx: RunContext[CopilotDeps], name: str,
                         description: str = "", config: dict | None = None) -> dict:
    """创建项目。config 可选项目级配置（普通 JSON 对象）。"""
    req = cpb.CreateProjectRequest(ctx=ctx.deps.ctx(), name=name, description=description)
    if config:
        req.config.update(config)
    r = await ctx.deps.sched.stub.CreateProject(req)
    return await to_dict_async(r)

_METHODS = {"GET": pb.HTTP_METHOD_GET, "POST": pb.HTTP_METHOD_POST, "PUT": pb.HTTP_METHOD_PUT,
            "DELETE": pb.HTTP_METHOD_DELETE, "PATCH": pb.HTTP_METHOD_PATCH,
            "HEAD": pb.HTTP_METHOD_HEAD, "OPTIONS": pb.HTTP_METHOD_OPTIONS}


@writes.tool(requires_approval=True)
async def create_api(ctx: RunContext[CopilotDeps], method: str, uri: str,
                     project_id: str | None = None,
                     headers: dict[str, str] | None = None,
                     params: dict[str, str] | None = None,
                     body: str = "") -> dict:
    """创建 HTTP 接口。method 为大写方法名；body 为原始文本（JSON 字符串）。
    project_id 省略（传 null）时使用页面左上角当前选择的项目。"""
    pid = ctx.deps.resolve_project_id(project_id)
    api = pb.HttpApi(method=_METHODS.get(method.upper(), pb.HTTP_METHOD_GET), uri=uri)
    for k, v in (headers or {}).items():
        api.headers.add(key=k, value=v)
    for k, v in (params or {}).items():
        api.params.add(key=k, value=v)
    if body:
        api.body.content_type = pb.BODY_CONTENT_TYPE_JSON
        api.body.raw = body
    r = await ctx.deps.sched.stub.CreateApi(
        cpb.CreateApiRequest(ctx=ctx.deps.ctx(), project_id=pid, http=api))
    return await to_dict_async(r)


@writes.tool(requires_approval=True)
async def create_grpc_api(ctx: RunContext[CopilotDeps],
                          full_service: str, method: str,
                          project_id: str | None = None,
                          request_message: dict | None = None,
                          metadata: dict[str, str] | None = None,
                          deadline_ms: int = 0) -> dict:
    """创建 gRPC 接口（执行走 server reflection，无需编译桩）。
    full_service 形如 package.Service；request_message 为 JSON 形态请求体。
    project_id 省略时使用页面左上角当前选择的项目。"""
    pid = ctx.deps.resolve_project_id(project_id)
    g = pb.GrpcApi(full_service=full_service, method=method)
    if request_message:
        from google.protobuf import json_format
        json_format.ParseDict(request_message, g.request_message)
    for k, v in (metadata or {}).items():
        g.metadata.add(key=k, value=v)
    if deadline_ms > 0:
        g.deadline.FromMilliseconds(deadline_ms)
    r = await ctx.deps.sched.stub.CreateApi(
        cpb.CreateApiRequest(ctx=ctx.deps.ctx(), project_id=pid, grpc=g))
    return await to_dict_async(r)


@writes.tool(requires_approval=True)
async def update_api(ctx: RunContext[CopilotDeps], api_id: str, api: dict,
                     kind: str = "http") -> dict:
    """修改已有接口。kind: http|grpc；api 为需要变更的字段（camelCase，如
    {"uri": "/v2/echo", "headers": {...}, "body": {...}}），未提供的字段保持原值。
    建议先 get_api 获取完整定义再改；敏感 header/cookie 未修改时不会被覆盖。"""
    k = str(kind or "http").strip().lower()
    if k not in ("http", "grpc"):
        raise ValueError(f"kind must be http or grpc, got {kind!r}")
    api_kind = cpb.API_KIND_HTTP if k == "http" else cpb.API_KIND_GRPC
    cur = await ctx.deps.sched.stub.GetApi(
        cpb.GetApiRequest(ctx=ctx.deps.ctx(), api_id=str(api_id), kind=api_kind))
    cur_d = await to_dict_async(cur)
    base = cur_d.get("http") if k == "http" else cur_d.get("grpc")
    if not isinstance(base, dict):
        raise ValueError(f"{k} api {api_id} not found")
    merged = {**base}
    for field, value in (api or {}).items():
        if value is None:
            continue
        if field in ("headers", "cookies") and _contains_redacted(value):
            continue  # get_api 返回的掩码值不能原样写回
        merged[field] = value

    if k == "http":
        h = pb.HttpApi()
        json_format_parse(merged, h)
        r = await ctx.deps.sched.stub.UpdateApi(
            cpb.UpdateApiRequest(ctx=ctx.deps.ctx(), api_id=str(api_id),
                                 kind=api_kind, http=h))
    else:
        g = pb.GrpcApi()
        json_format_parse(merged, g)
        r = await ctx.deps.sched.stub.UpdateApi(
            cpb.UpdateApiRequest(ctx=ctx.deps.ctx(), api_id=str(api_id),
                                 kind=api_kind, grpc=g))
    return await to_dict_async(r)


@writes.tool(requires_approval=True)
async def create_test_case(ctx: RunContext[CopilotDeps], name: str,
                           definition: dict, case_type: str = "declarative",
                           project_id: str | None = None,
                           description: str = "") -> dict:
    """创建测试用例。case_type: declarative（definition=DeclarativeCase 的 JSON：{"steps":[...]}）
    或 lowcode（definition={"source": "...", "entry": "run",
    "http_api_refs": ["接口ID", ...], "grpc_api_refs": [...]}）。
    project_id 省略时使用页面左上角当前选择的项目。"""
    pid = ctx.deps.resolve_project_id(project_id)
    case = pb.TestCase(name=name, description=description, created_by="copilot")
    if case_type == "lowcode":
        case.type = pb.TEST_CASE_TYPE_LOWCODE
        lc = pb.LowCodeCase()
        lc.source = definition.get("source", "")
        lc.entry = definition.get("entry", "run")
        # 依赖声明必须透传（此前只拷 source/entry，显式 refs 会被静默丢弃，
        # 运行时才报 "not in http_api_refs"）。兼容 camelCase 前端命名。
        lc.http_api_refs.extend(
            str(x) for x in definition.get("http_api_refs")
            or definition.get("httpApiRefs") or [])
        lc.grpc_api_refs.extend(
            str(x) for x in definition.get("grpc_api_refs")
            or definition.get("grpcApiRefs") or [])
        case.lowcode.CopyFrom(lc)
    else:
        case.type = pb.TEST_CASE_TYPE_DECLARATIVE
        dc = pb.DeclarativeCase()
        json_format_parse(definition, dc)
        case.declarative.CopyFrom(dc)
    r = await ctx.deps.sched.stub.CreateTestCase(
        cpb.CreateTestCaseRequest(ctx=ctx.deps.ctx(), project_id=pid, case=case))
    return await to_dict_async(r)


@writes.tool(requires_approval=True)
async def update_test_case(ctx: RunContext[CopilotDeps], case_id: str,
                           name: str | None = None,
                           description: str | None = None,
                           definition: dict | None = None) -> dict:
    """修改已有测试用例。只更新显式传入的字段；definition 与现有定义浅合并，
    因此只改 lowcode source / declarative steps 时无需重复 httpApiRefs 等字段。
    不能修改用例 type（要换类型请新建用例）。"""
    cur = await ctx.deps.sched.stub.GetTestCase(
        cpb.GetTestCaseRequest(ctx=ctx.deps.ctx(), case_id=str(case_id)))
    cur_d = await to_dict_async(cur)
    current = cur_d.get("case")
    if not isinstance(current, dict):
        raise ValueError(f"test case {case_id} not found")

    try:
        case_type = pb.TestCaseType.Value(current.get("type", "TEST_CASE_TYPE_UNSPECIFIED"))
    except ValueError:
        case_type = pb.TEST_CASE_TYPE_UNSPECIFIED
    if case_type not in (pb.TEST_CASE_TYPE_DECLARATIVE, pb.TEST_CASE_TYPE_LOWCODE):
        raise ValueError(f"unsupported test case type: {current.get('type')!r}")

    if name is None:
        name = current.get("name") or ""
    if description is None:
        description = current.get("description") or ""
    # GetTestCaseResponse 的 oneof 在 JSON 中键为 lowcode / declarative
    base_def = current.get("lowcode") if case_type == pb.TEST_CASE_TYPE_LOWCODE \
        else current.get("declarative")
    if definition is None:
        merged_def = base_def or {}
    else:
        merged_def = {**(base_def or {}), **(definition or {})}

    case = pb.TestCase(type=case_type, name=name, description=description)
    if case_type == pb.TEST_CASE_TYPE_LOWCODE:
        lc = pb.LowCodeCase()
        json_format_parse(merged_def, lc)
        case.lowcode.CopyFrom(lc)
    else:
        dc = pb.DeclarativeCase()
        json_format_parse(merged_def, dc)
        case.declarative.CopyFrom(dc)

    r = await ctx.deps.sched.stub.UpdateTestCase(
        cpb.UpdateTestCaseRequest(ctx=ctx.deps.ctx(), case_id=str(case_id), case=case))
    return await to_dict_async(r)


def json_format_parse(d: dict, msg) -> None:
    from google.protobuf import json_format
    json_format.ParseDict(d, msg, ignore_unknown_fields=False)


# ---------------------------------------------------------------------------
# Playwright UI 用例生成：结构化 steps → 声明式 UI_ACTION 步骤树 / 低代码 ctx.page
# ---------------------------------------------------------------------------

_UI_ACTION_ENUM = {
    "goto": pb.UI_ACTION_GOTO, "click": pb.UI_ACTION_CLICK, "fill": pb.UI_ACTION_FILL,
    "select": pb.UI_ACTION_SELECT, "check": pb.UI_ACTION_CHECK, "uncheck": pb.UI_ACTION_CHECK,
    "hover": pb.UI_ACTION_HOVER, "press": pb.UI_ACTION_PRESS,
    "expect_text": pb.UI_ACTION_EXPECT_TEXT, "expect_visible": pb.UI_ACTION_EXPECT_VISIBLE,
    "screenshot": pb.UI_ACTION_SCREENSHOT, "wait": pb.UI_ACTION_WAIT,
    "download": pb.UI_ACTION_DOWNLOAD,
}
# 每个 action 必填字段；wait 的 value 统一为毫秒（低代码生成器转为 ctx.page.wait_for，
# 声明式生成器转为 UI_ACTION_WAIT 的秒）。
_UI_STEP_REQUIRED: dict[str, tuple[str, ...]] = {
    "goto": ("target",),
    "click": ("target",),
    "fill": ("target", "value"),
    "select": ("target", "value"),
    "check": ("target",),
    "uncheck": ("target",),
    "hover": ("target",),
    "press": (),
    "expect_text": ("target", "value"),
    "expect_visible": ("target",),
    "wait": ("value",),
    "screenshot": (),
    "download": ("target",),
}
# 低代码桥不渲染模板（与声明式引擎不同），生成器把 {{vars.x}} / {{parameters.x}}
# 转为 ctx.vars / ctx.parameters 访问；其余平台模板语法保持不变。
_TEMPLATE_RE = re.compile(
    r"\{\{\s*(vars|parameters)\s*\.\s*([A-Za-z_]\w*(?:\s*\.\s*[A-Za-z_]\w*)*)\s*\}\}")
# 用于找出任意 {{...}}：识别不了（表达式/索引等）就明确报错，绝不把模板字面量
# 静默写进低代码脚本（否则运行时既不渲染也不会报语法错，断言/输入会得到错误值）。
_ANY_TEMPLATE_RE = re.compile(r"\{\{.*?\}\}", re.S)


def _normalize_ui_steps(steps: Any) -> list[dict[str, Any]]:
    if not isinstance(steps, list) or not steps:
        raise ValueError("steps 不能为空，至少需要一个 UI 动作")
    out: list[dict[str, Any]] = []
    for i, raw in enumerate(steps):
        if not isinstance(raw, dict):
            raise ValueError(f"steps[{i}] 必须是对象")
        action = str(raw.get("action", "")).strip().lower()
        if action not in _UI_ACTION_ENUM:
            allowed = ", ".join(sorted(_UI_ACTION_ENUM))
            raise ValueError(f"steps[{i}].action 不合法：{action!r}；可用动作：{allowed}")
        target = str(raw.get("target") or "").strip()
        value = raw.get("value")
        for field in _UI_STEP_REQUIRED[action]:
            if field == "target" and not target:
                raise ValueError(f"steps[{i}]（{action}）缺少 target")
            if field == "value" and value in (None, ""):
                raise ValueError(f"steps[{i}]（{action}）缺少 value")
        full_page = raw.get("full_page", True)
        if isinstance(full_page, str):
            full_page = full_page.strip().lower() not in ("false", "0", "no", "off")
        out.append({
            "action": action,
            "target": target,
            "value": value,
            "full_page": bool(full_page),
        })
    return out


def _py_path_expr(root: str, path: str) -> str:
    parts = [p.strip() for p in path.split(".") if p.strip()]
    if not parts:
        raise ValueError(f"变量引用 {{...}} 路径为空：{path!r}")
    return f"ctx.{root}[" + "][".join(json.dumps(p, ensure_ascii=False) for p in parts) + "]"


def _validate_lowcode_templates(text: str) -> None:
    """低代码只支持点路径模板；其他表达式模板直接报错，避免把未渲染的
    `{{...}}` 字面量静默写进脚本（运行时不报语法错，但值一定不对）。"""
    for m in _ANY_TEMPLATE_RE.finditer(text):
        if _TEMPLATE_RE.fullmatch(m.group(0)) is None:
            raise ValueError(
                "lowcode 模式仅支持 {{vars.a.b}} / {{parameters.a.b}} 模板，"
                f"无法转换表达式模板 {m.group(0)!r}；请改用 ctx.vars / ctx.parameters "
                "Python 表达式，或选择 declarative 模式")


def _lowcode_text(value: Any) -> str:
    """字符串参数 → Python 表达式；纯文本用 JSON 双引号字面量，含 {{vars.x}} 模板时转为拼接式。"""
    text = str(value)
    _validate_lowcode_templates(text)
    if not _TEMPLATE_RE.search(text):
        return json.dumps(text, ensure_ascii=False)
    pieces: list[str] = []
    last = 0
    for m in _TEMPLATE_RE.finditer(text):
        if m.start() > last:
            pieces.append(json.dumps(text[last:m.start()], ensure_ascii=False))
        pieces.append(f"str({_py_path_expr(m.group(1), m.group(2))})")
        last = m.end()
    if last < len(text):
        pieces.append(json.dumps(text[last:], ensure_ascii=False))
    if len(pieces) == 1:
        return pieces[0]
    return "(" + " + ".join(pieces) + ")"


def _lowcode_wait_ms(value: Any) -> str:
    text = str(value).strip()
    _validate_lowcode_templates(text)
    m = _TEMPLATE_RE.fullmatch(text)
    if m:
        return f"int({_py_path_expr(m.group(1), m.group(2))})"
    try:
        return str(int(text))
    except ValueError as e:
        raise ValueError("wait 的 value 必须是毫秒整数（如 1000）或 {{parameters.wait_ms}}") from e


def render_lowcode_ui_source(start_url: str, steps: Any) -> str:
    """把 UI 步骤渲染为 lowcode case_type 的 Python 源码（仅使用 ctx.page）。"""
    if not str(start_url).strip():
        raise ValueError("start_url 不能为空")
    normalized = _normalize_ui_steps(steps)
    lines = ["from testpilot_sdk import Context", "", "", "async def run(ctx):"]
    # LLM 常把 start_url 同时作为首个 goto 步骤；与首步完全相同时不重复导航。
    if not (normalized and normalized[0]["action"] == "goto"
            and normalized[0]["target"] == str(start_url).strip()):
        lines.append(f"    await ctx.page.goto({_lowcode_text(start_url)})")
    for s in normalized:
        action = s["action"]
        target = _lowcode_text(s["target"]) if s["target"] else '""'
        value = s["value"]
        if action == "goto":
            lines.append(f"    await ctx.page.goto({target})")
        elif action == "click":
            lines.append(f"    await ctx.page.click({target})")
        elif action == "fill":
            lines.append(f"    await ctx.page.fill({target}, {_lowcode_text(value)})")
        elif action == "select":
            lines.append(f"    await ctx.page.select({target}, {_lowcode_text(value)})")
        elif action == "check":
            lines.append(f"    await ctx.page.check({target})")
        elif action == "uncheck":
            lines.append(f"    await ctx.page.uncheck({target})")
        elif action == "hover":
            lines.append(f"    await ctx.page.hover({target})")
        elif action == "press":
            lines.append(f"    await ctx.page.press({target}, {_lowcode_text(value or 'Enter')})")
        elif action == "expect_text":
            lines.append(
                f"    await ctx.page.expect_text({target}, {_lowcode_text(value)})")
        elif action == "expect_visible":
            hidden = str(value or "").strip().lower() in ("hidden", "false", "0")
            method = "expect_hidden" if hidden else "expect_visible"
            lines.append(f"    await ctx.page.{method}({target})")
        elif action == "wait":
            wait_ms = _lowcode_wait_ms(value)
            if s["target"]:
                lines.append(
                    f"    await ctx.page.wait_for_selector({target}, "
                    f"timeout_ms={wait_ms})")
            else:
                lines.append(f"    await ctx.page.wait_for({wait_ms})")
        elif action == "screenshot":
            lines.append(f"    await ctx.page.screenshot(full_page={s['full_page']!r})")
        elif action == "download":
            name = ("" if value in (None, "")
                    else f", name={_lowcode_text(value)}")
            lines.append(f"    await ctx.page.download({target}{name})")
        else:  # 防御：_normalize_ui_steps 已校验，不会到这里
            raise ValueError(f"unknown ui action: {action}")
    lines.append("")
    return "\n".join(lines)


def _decl_value(s: dict[str, Any]) -> str:
    """声明式 UI_ACTION 的 value 字段（平台引擎负责 {{...}} 模板展开）。"""
    action = s["action"]
    value = s["value"]
    if action == "goto":
        return str(value or "")
    if action in ("fill", "select", "expect_text"):
        return str(value)
    if action == "press":
        return str(value or "Enter")
    if action == "check":
        return "false" if str(value or "").lower() in ("false", "0", "off", "uncheck") else "true"
    if action == "uncheck":
        return "false"
    if action == "wait":
        text = str(value).strip()
        m = _TEMPLATE_RE.fullmatch(text)
        if m:
            # 工具约定 value 为毫秒，声明式 UI_ACTION_WAIT 按秒解释 → 模板内 /1000
            return "{{" + f"{m.group(1)}.{m.group(2)} / 1000" + "}}"
        try:
            return str(int(text) / 1000)
        except ValueError:
            raise ValueError("wait 的 value 必须是毫秒整数（如 1000）或 {{parameters.wait_ms}}")
    if action == "screenshot":
        return "full" if s["full_page"] else ""
    if action == "expect_visible":
        return "hidden" if str(value or "").lower() in ("hidden", "false", "0") else ""
    return str(value or "")


def build_declarative_ui_case(start_url: str, steps: Any) -> pb.DeclarativeCase:
    """把 UI 步骤渲染为声明式用例：首个 UI_ACTION GOTO + 后续 UI_ACTION 步骤。"""
    if not str(start_url).strip():
        raise ValueError("start_url 不能为空")
    normalized = _normalize_ui_steps(steps)
    dc = pb.DeclarativeCase()
    # LLM 常把 start_url 同时作为首个 goto 步骤；与首步完全相同时不重复导航。
    if not (normalized and normalized[0]["action"] == "goto"
            and normalized[0]["target"] == str(start_url).strip()):
        first = dc.steps.add()
        first.type = pb.STEP_TYPE_UI_ACTION
        first.name = "打开页面"
        first.ui_action.action = pb.UI_ACTION_GOTO
        first.ui_action.target = str(start_url)
    for s in normalized:
        step = dc.steps.add()
        step.type = pb.STEP_TYPE_UI_ACTION
        action = s["action"]
        step.name = f"{action} {s['target'] or s['value'] or ''}"[:200]
        step.ui_action.action = _UI_ACTION_ENUM[action]
        step.ui_action.target = s["target"]
        step.ui_action.value = _decl_value(s)
    return dc


@writes.tool(requires_approval=True)
async def create_ui_test_case(ctx: RunContext[CopilotDeps], name: str,
                              start_url: str, steps: list[dict],
                              project_id: str | None = None,
                              description: str = "",
                              case_type: str = "declarative",
                              parameters: dict | None = None) -> dict:
    """创建 Playwright UI 测试用例（写操作，需审批）。

    start_url：打开页面的 URL；相对路径（如 /login）基于运行环境 base_url。
    steps：有序 UI 动作数组，每个元素 {"action": ..., "target": "...", "value": "..."}。
    支持动作：goto/click/fill/select/check/uncheck/hover/press/expect_text/
    expect_visible/wait/screenshot/download。
    - target 为 Playwright locator（CSS 或 XPath），fill/select 等还需 value；
    - wait 的 value 是毫秒整数（如 1000）；wait 带 target 时表示等待 selector；
    - expect_visible 的 value 为 "hidden"/"false"/"0" 时断言元素不可见；
    - download 的 value 可选：保存文件名（不带 value 时使用响应头文件名）；
    - screenshot 可用 "full_page": false；
    - target/value 中可写 {{vars.xxx}} 引用环境变量；{{parameters.xxx}} 仅
      lowcode 模式可用（默认值放在 parameters 参数，脚本经 ctx.parameters 读取）；
      lowcode 模式仅支持点路径模板（如 {{vars.user.name}}），复杂表达式请
      用 ctx.vars / ctx.parameters 编写，或改用 declarative 模式。
    case_type：declarative（默认，生成可视化 UI_ACTION 步骤树）或
    lowcode（生成 ctx.page Python 脚本，流程含循环/条件/复杂变量时选用）。
    parameters：可选对象；case_type=lowcode 时写入 LowCodeCase.parameters
    （脚本中 ctx.parameters 的默认值），declarative 模式不使用。
    project_id 省略时使用页面左上角当前选择的项目。"""
    pid = ctx.deps.resolve_project_id(project_id)
    case = pb.TestCase(name=name, description=description, created_by="copilot")
    kind = str(case_type or "declarative").strip().lower()
    if kind == "lowcode":
        case.type = pb.TEST_CASE_TYPE_LOWCODE
        lc = pb.LowCodeCase()
        lc.source = render_lowcode_ui_source(start_url, steps)
        lc.entry = "run"
        if parameters:
            lc.parameters.update(parameters)
        case.lowcode.CopyFrom(lc)
    elif kind == "declarative":
        case.type = pb.TEST_CASE_TYPE_DECLARATIVE
        case.declarative.CopyFrom(build_declarative_ui_case(start_url, steps))
    else:
        raise ValueError("case_type 只能是 declarative 或 lowcode")
    r = await ctx.deps.sched.stub.CreateTestCase(
        cpb.CreateTestCaseRequest(ctx=ctx.deps.ctx(), project_id=pid, case=case))
    return await to_dict_async(r)


@writes.tool(requires_approval=True)
async def create_test_plan(ctx: RunContext[CopilotDeps], name: str,
                           case_ids: list[str],
                           project_id: str | None = None,
                           env_id: str | None = None,
                           timeout_ms: int = 300000) -> dict:
    """创建测试计划（按序引用用例）。
    project_id/env_id 省略时使用页面左上角当前选择的项目/环境。"""
    pid = ctx.deps.resolve_project_id(project_id)
    eid = ctx.deps.resolve_env_id(env_id, required=True)
    plan = pb.TestPlan(name=name, env_id=eid)
    plan.timeout.FromMilliseconds(timeout_ms)
    for cid in case_ids:
        item = plan.items.add()
        item.case_id = cid
        item.enabled = True
    r = await ctx.deps.sched.stub.CreateTestPlan(
        cpb.CreateTestPlanRequest(ctx=ctx.deps.ctx(), project_id=pid, plan=plan))
    return await to_dict_async(r)


@writes.tool(requires_approval=True)
async def import_openapi(ctx: RunContext[CopilotDeps],
                         openapi_document: str = "", openapi_url: str = "",
                         project_id: str | None = None) -> dict:
    """导入 OpenAPI 3 文档，批量生成接口。二选一：openapi_document（JSON/YAML 原文）
    或 openapi_url（文档 URL，服务端拉取；环回/私网地址会被拒绝）。
    project_id 省略时使用页面左上角当前选择的项目。"""
    req = cpb.ImportOpenApiRequest(ctx=ctx.deps.ctx(),
                                   project_id=ctx.deps.resolve_project_id(project_id))
    if openapi_url:
        req.openapi_url = openapi_url
    elif openapi_document:
        req.openapi_document = openapi_document
    else:
        raise ValueError("openapi_document 与 openapi_url 需提供一个")
    r = await ctx.deps.sched.stub.ImportOpenApi(req)
    return await to_dict_async(r)


@writes.tool(requires_approval=True)
async def apply_openapi_diff(ctx: RunContext[CopilotDeps],
                             openapi_document: str,
                             project_id: str | None = None,
                             auto_update_cases: bool = False) -> dict:
    """以新 spec 增量更新项目接口（按 method+uri 匹配）：新增→创建、变更→更新、
    缺失→仅报告不删除（kind=breaking 表示参数键被移除或 body content_type 变化）。
    auto_update_cases=true 时，引用变更接口的用例会把新快照内联写回 definition。
    project_id 省略时使用页面左上角当前选择的项目。"""
    pid = ctx.deps.resolve_project_id(project_id)
    r = await ctx.deps.sched.stub.ApplyOpenApiDiff(cpb.ApplyOpenApiDiffRequest(
        ctx=ctx.deps.ctx(), project_id=pid,
        openapi_document=openapi_document, auto_update_cases=auto_update_cases))
    return await to_dict_async(r)


@writes.tool(requires_approval=True)
async def trigger_run(ctx: RunContext[CopilotDeps], plan_id: str,
                      env_id: str | None = None) -> dict:
    """触发一次测试计划运行，返回 run_id（可用 get_run 查询结果）。
    env_id 省略时使用页面左上角当前选择的环境（未选择则沿用计划默认环境）。"""
    eid = ctx.deps.resolve_env_id(env_id, required=False)
    r = await ctx.deps.sched.stub.TriggerRun(
        cpb.TriggerRunRequest(ctx=ctx.deps.ctx(), plan_id=plan_id, env_id=eid))
    return await to_dict_async(r)


@writes.tool(requires_approval=True)
async def trigger_stress(ctx: RunContext[CopilotDeps], stress_plan_id: str,
                         env_id: str | None = None) -> dict:
    """触发一次压测运行。env_id 省略时使用页面左上角当前选择的环境。"""
    eid = ctx.deps.resolve_env_id(env_id, required=False)
    r = await ctx.deps.sched.stub.TriggerStress(
        cpb.TriggerStressRequest(ctx=ctx.deps.ctx(), stress_plan_id=stress_plan_id,
                                 env_id=eid))
    return await to_dict_async(r)


# parse_struct 占位引用（部分响应含 Struct 时备用）
_ = parse_struct
_ = Any


# ---------------------------------------------------------------------------
# UI 探测工具（v1，docs/ui-probe-design.md §4.4）：
# 会话生命周期/快照只读免审批；open/act/eval 有资源或副作用，走 HITL 审批。
# 会话空闲 TTL 由 Scheduler 回收；快照在工具结果层二次截断（上下文预算）。
# ---------------------------------------------------------------------------

probe = FunctionToolset()


def _clip_probe_snapshot(d: dict) -> dict:
    """工具结果二次截断（TP_COPILOT_PROBE_SNAPSHOT_MAX_BYTES，默认 16KB）：
    Scheduler/Worker 已各有一道截断，这里兜底控制 LLM 上下文预算。"""
    snap = d.get("ariaSnapshot")
    if not snap:
        return d
    try:
        maxb = int(os.environ.get("TP_COPILOT_PROBE_SNAPSHOT_MAX_BYTES") or 16384)
    except ValueError:
        maxb = 16384
    raw = snap.encode("utf-8")
    if len(raw) > maxb:
        tail = "\n… [已截断]"
        cut = raw[:max(0, maxb - len(tail.encode("utf-8")))]
        d["ariaSnapshot"] = cut.decode("utf-8", "ignore") + tail
        d["snapshotTruncated"] = True
    return d


@probe.tool(requires_approval=True)
async def ui_probe_open(ctx: RunContext[CopilotDeps], url: str,
                        env_id: str | None = None) -> dict:
    """打开 UI 探测会话并返回页面 ARIA 快照（写操作，需审批）。

    返回的快照是页面可交互元素的结构化文本（role/name/层级），据此选择 locator，
    不要凭空猜测 selector。url 用相对路径（基于环境 base_url）或 http(s) 绝对地址；
    相对路径必须已选择环境（env_id 或页面左上角当前环境）。
    会话空闲由 Scheduler 自动回收；探测结束应调用 ui_probe_close。"""
    if not ctx.deps.probe_session_id:
        raise ValueError("UI 探测会话不可用（probe_session_id 未注入）")
    eid = ctx.deps.resolve_env_id(env_id, required=False)
    r = await ctx.deps.sched.stub.OpenProbe(cpb.OpenProbeRequest(
        ctx=ctx.deps.ctx(), session_id=ctx.deps.probe_session_id,
        url=url, env_id=eid))
    return _clip_probe_snapshot(await to_dict_async(r))


@probe.tool
async def ui_probe_snapshot(ctx: RunContext[CopilotDeps], ref: str = "") -> dict:
    """获取当前页面 ARIA 快照（免审批）。跳转/弹窗后可反复调用观察页面变化；
    ref 为子树定位（v1 暂不支持全页之外的聚焦，传空即可）。"""
    r = await ctx.deps.sched.stub.GetProbeSnapshot(cpb.GetProbeSnapshotRequest(
        ctx=ctx.deps.ctx(), session_id=ctx.deps.probe_session_id, ref=ref))
    return _clip_probe_snapshot(await to_dict_async(r))


@probe.tool(requires_approval=True)
async def ui_probe_act(ctx: RunContext[CopilotDeps], action: str,
                       target: str = "", value: str = "") -> dict:
    """在探测会话上执行单步 UI 动作（写操作，需审批），执行后自动返回新快照。

    action: goto/click/fill/select/check/hover/press/wait；target 为 Playwright
    locator；失败时 error 字段是 Playwright 原始报错（含等待超时与元素状态），
    应据此修正 locator 重试，而不是换随机 selector。"""
    norm = _normalize_ui_steps([{"action": action, "target": target, "value": value}])
    st = norm[0]
    r = await ctx.deps.sched.stub.ActProbe(cpb.ActProbeRequest(
        ctx=ctx.deps.ctx(), session_id=ctx.deps.probe_session_id,
        action=pb.UiActionStep(action=_UI_ACTION_ENUM[st["action"]],
                               target=st["target"], value=_decl_value(st))))
    return _clip_probe_snapshot(await to_dict_async(r))


@probe.tool(requires_approval=True)
async def ui_probe_eval(ctx: RunContext[CopilotDeps], expression: str) -> dict:
    """在页面上下文执行 JS 并返回 JSON 结果（写操作，需审批）。

    用于固定动作覆盖不了的探测：枚举候选元素、检查元素状态/属性、多策略验证
    locator、读取页面状态。结果有体积上限，只取需要的字段，避免返回整个 DOM。"""
    r = await ctx.deps.sched.stub.EvalProbe(cpb.EvalProbeRequest(
        ctx=ctx.deps.ctx(), session_id=ctx.deps.probe_session_id,
        expression=expression))
    return await to_dict_async(r)


@probe.tool
async def ui_probe_close(ctx: RunContext[CopilotDeps]) -> dict:
    """关闭 UI 探测会话（免审批）。探测确认完整流程、开始生成用例前应调用。"""
    r = await ctx.deps.sched.stub.CloseProbe(cpb.CloseProbeRequest(
        ctx=ctx.deps.ctx(), session_id=ctx.deps.probe_session_id))
    return await to_dict_async(r)
