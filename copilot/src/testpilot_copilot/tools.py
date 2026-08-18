"""Copilot 工具集：只读（免审批）+ 写/触发（requires_approval → HITL）。

工具经 Scheduler gRPC 执行，租户/用户上下文来自每次请求的 deps。
"""

from __future__ import annotations

import logging
import re
from dataclasses import dataclass
from typing import Any

import httpx
from pydantic_ai import RunContext
from pydantic_ai.toolsets import FunctionToolset

from testpilot.common.v1 import types_pb2 as pb
from testpilot.copilot.v1 import copilot_pb2 as cpb

from .scheduler_client import SchedulerClient, parse_struct, to_dict

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

        if self.ui_project_id:
            try:
                r = await self.http.get(f"/api/v1/projects/{self.ui_project_id}", headers=h)
                if r.status_code == 200:
                    self.ui_project = r.json()
                else:
                    log.warning("copilot ui project %s not found: HTTP %s",
                                self.ui_project_id, r.status_code)
            except (httpx.HTTPError, ValueError) as e:
                log.warning("hydrate copilot ui project failed: %s", e)
        if self.ui_project is None:
            self.ui_project_id = ""

        if self.ui_project_id and self.ui_env_id:
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
    return to_dict(r).get("projects", [])


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
    apis = to_dict(r).get("httpApis", [])
    for a in apis:
        _redact_headers(a.get("headers"))
        _redact_cookies(a.get("cookies"))
    return apis


@readonly.tool
async def get_api(ctx: RunContext[CopilotDeps], api_id: str) -> dict:
    """获取单个接口详情。"""
    r = await ctx.deps.sched.stub.GetApi(
        cpb.GetApiRequest(ctx=ctx.deps.ctx(), api_id=api_id, kind=cpb.API_KIND_HTTP))
    d = to_dict(r)
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
    return to_dict(r).get("environments", [])


@readonly.tool
async def list_test_cases(ctx: RunContext[CopilotDeps],
                          project_id: str | None = None, query: str = "") -> list[dict]:
    """列出测试用例（声明式/低代码）。project_id 省略时使用页面左上角当前选择的项目。"""
    pid = ctx.deps.resolve_project_id(project_id)
    r = await ctx.deps.sched.stub.ListTestCases(
        cpb.ListTestCasesRequest(ctx=ctx.deps.ctx(), project_id=pid, query=query,
                                 page=pb.PageRequest(page_size=200)))
    return to_dict(r).get("cases", [])


@readonly.tool
async def get_test_case(ctx: RunContext[CopilotDeps], case_id: str) -> dict:
    """获取用例完整定义（步骤树/低代码脚本）。"""
    r = await ctx.deps.sched.stub.GetTestCase(
        cpb.GetTestCaseRequest(ctx=ctx.deps.ctx(), case_id=case_id))
    return to_dict(r)


@readonly.tool
async def query_schema(ctx: RunContext[CopilotDeps], topic: str = "") -> dict:
    """查询领域 schema（数据字典：实体/字段/枚举），写用例前先查。"""
    r = await ctx.deps.sched.stub.QuerySchema(
        cpb.QuerySchemaRequest(ctx=ctx.deps.ctx(), topic=topic))
    return to_dict(r)


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
    return to_dict(r).get("runs", [])


@readonly.tool
async def get_run(ctx: RunContext[CopilotDeps], run_id: str, include_steps: bool = False) -> dict:
    """获取运行详情（含用例结果；include_steps=true 含步骤级明细，用于失败根因分析）。"""
    r = await ctx.deps.sched.stub.GetRun(
        cpb.GetRunRequest(ctx=ctx.deps.ctx(), run_id=run_id, include_steps=include_steps))
    return to_dict(r)


@readonly.tool
async def query_coverage(ctx: RunContext[CopilotDeps],
                         project_id: str | None = None) -> dict:
    """接口 vs 用例覆盖率分析（未覆盖接口清单）。
    project_id 省略时使用页面左上角当前选择的项目。"""
    pid = ctx.deps.resolve_project_id(project_id)
    r = await ctx.deps.sched.stub.QueryCoverage(
        cpb.QueryCoverageRequest(ctx=ctx.deps.ctx(), project_id=pid))
    return to_dict(r)


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
    return to_dict(r)


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
    return to_dict(r)


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
    return to_dict(r)

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
    return to_dict(r)


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
    return to_dict(r)


@writes.tool(requires_approval=True)
async def create_test_case(ctx: RunContext[CopilotDeps], name: str,
                           definition: dict, case_type: str = "declarative",
                           project_id: str | None = None,
                           description: str = "") -> dict:
    """创建测试用例。case_type: declarative（definition=DeclarativeCase 的 JSON：{"steps":[...]}）
    或 lowcode（definition={"source": "...", "entry": "run"}）。
    project_id 省略时使用页面左上角当前选择的项目。"""
    pid = ctx.deps.resolve_project_id(project_id)
    case = pb.TestCase(name=name, description=description, created_by="copilot")
    if case_type == "lowcode":
        case.type = pb.TEST_CASE_TYPE_LOWCODE
        lc = pb.LowCodeCase()
        lc.source = definition.get("source", "")
        lc.entry = definition.get("entry", "run")
        case.lowcode.CopyFrom(lc)
    else:
        case.type = pb.TEST_CASE_TYPE_DECLARATIVE
        dc = pb.DeclarativeCase()
        json_format_parse(definition, dc)
        case.declarative.CopyFrom(dc)
    r = await ctx.deps.sched.stub.CreateTestCase(
        cpb.CreateTestCaseRequest(ctx=ctx.deps.ctx(), project_id=pid, case=case))
    return to_dict(r)


def json_format_parse(d: dict, msg) -> None:
    from google.protobuf import json_format
    json_format.ParseDict(d, msg, ignore_unknown_fields=False)


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
    return to_dict(r)


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
    return to_dict(r)


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
    return to_dict(r)


@writes.tool(requires_approval=True)
async def trigger_run(ctx: RunContext[CopilotDeps], plan_id: str,
                      env_id: str | None = None) -> dict:
    """触发一次测试计划运行，返回 run_id（可用 get_run 查询结果）。
    env_id 省略时使用页面左上角当前选择的环境（未选择则沿用计划默认环境）。"""
    eid = ctx.deps.resolve_env_id(env_id, required=False)
    r = await ctx.deps.sched.stub.TriggerRun(
        cpb.TriggerRunRequest(ctx=ctx.deps.ctx(), plan_id=plan_id, env_id=eid))
    return to_dict(r)


@writes.tool(requires_approval=True)
async def trigger_stress(ctx: RunContext[CopilotDeps], stress_plan_id: str,
                         env_id: str | None = None) -> dict:
    """触发一次压测运行。env_id 省略时使用页面左上角当前选择的环境。"""
    eid = ctx.deps.resolve_env_id(env_id, required=False)
    r = await ctx.deps.sched.stub.TriggerStress(
        cpb.TriggerStressRequest(ctx=ctx.deps.ctx(), stress_plan_id=stress_plan_id,
                                 env_id=eid))
    return to_dict(r)


# parse_struct 占位引用（部分响应含 Struct 时备用）
_ = parse_struct
_ = Any
