"""Copilot 工具集：只读（免审批）+ 写/触发（requires_approval → HITL）。

工具经 Scheduler gRPC 执行，租户/用户上下文来自每次请求的 deps。
"""

from __future__ import annotations

import re
from dataclasses import dataclass
from typing import Any

import httpx
from pydantic_ai import RunContext
from pydantic_ai.toolsets import FunctionToolset

from testpilot.common.v1 import types_pb2 as pb
from testpilot.copilot.v1 import copilot_pb2 as cpb

from .scheduler_client import SchedulerClient, parse_struct, to_dict

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


@dataclass
class CopilotDeps:
    sched: SchedulerClient
    tenant_id: int
    user_id: str
    http: httpx.AsyncClient      # scheduler REST（会话持久化）
    token: str

    def ctx(self) -> pb.RequestContext:
        return SchedulerClient.ctx(self.tenant_id, self.user_id)


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


@readonly.tool
async def list_apis(ctx: RunContext[CopilotDeps], project_id: str, query: str = "") -> list[dict]:
    """列出项目下的 HTTP 接口（id/method/uri/headers/params/body）。"""
    r = await ctx.deps.sched.stub.ListApis(
        cpb.ListApisRequest(ctx=ctx.deps.ctx(), project_id=project_id, query=query,
                            page=pb.PageRequest(page_size=200)))
    apis = to_dict(r).get("httpApis", [])
    for a in apis:
        _redact_headers(a.get("headers"))
    return apis


@readonly.tool
async def get_api(ctx: RunContext[CopilotDeps], api_id: str) -> dict:
    """获取单个接口详情。"""
    r = await ctx.deps.sched.stub.GetApi(
        cpb.GetApiRequest(ctx=ctx.deps.ctx(), api_id=api_id, kind=cpb.API_KIND_HTTP))
    d = to_dict(r)
    _redact_headers(d.get("http", {}).get("headers"))
    return d


@readonly.tool
async def list_environments(ctx: RunContext[CopilotDeps], project_id: str) -> list[dict]:
    """列出项目环境（含 base_url）。"""
    r = await ctx.deps.sched.stub.ListEnvironments(
        cpb.ListEnvironmentsRequest(ctx=ctx.deps.ctx(), project_id=project_id))
    return to_dict(r).get("environments", [])


@readonly.tool
async def list_test_cases(ctx: RunContext[CopilotDeps], project_id: str, query: str = "") -> list[dict]:
    """列出测试用例（声明式/低代码）。"""
    r = await ctx.deps.sched.stub.ListTestCases(
        cpb.ListTestCasesRequest(ctx=ctx.deps.ctx(), project_id=project_id, query=query,
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
async def list_runs(ctx: RunContext[CopilotDeps], project_id: str = "",
                    plan_id: str = "", status: str = "") -> list[dict]:
    """列出测试运行记录。status 可选：RUN_STATUS_PASSED/FAILED/RUNNING 等。"""
    st = pb.RunStatus.Value(status) if status else pb.RUN_STATUS_UNSPECIFIED
    r = await ctx.deps.sched.stub.ListRuns(
        cpb.ListRunsRequest(ctx=ctx.deps.ctx(), project_id=project_id, plan_id=plan_id,
                            status=st, page=pb.PageRequest(page_size=50)))
    return to_dict(r).get("runs", [])


@readonly.tool
async def get_run(ctx: RunContext[CopilotDeps], run_id: str, include_steps: bool = False) -> dict:
    """获取运行详情（含用例结果；include_steps=true 含步骤级明细，用于失败根因分析）。"""
    r = await ctx.deps.sched.stub.GetRun(
        cpb.GetRunRequest(ctx=ctx.deps.ctx(), run_id=run_id, include_steps=include_steps))
    return to_dict(r)


@readonly.tool
async def query_coverage(ctx: RunContext[CopilotDeps], project_id: str) -> dict:
    """接口 vs 用例覆盖率分析（未覆盖接口清单）。"""
    r = await ctx.deps.sched.stub.QueryCoverage(
        cpb.QueryCoverageRequest(ctx=ctx.deps.ctx(), project_id=project_id))
    return to_dict(r)


# ---------------------------------------------------------------------------
# 写/触发工具（requires_approval → 前端 HITL 审批后执行，Scheduler 落审计）
# ---------------------------------------------------------------------------

writes = FunctionToolset()

_METHODS = {"GET": pb.HTTP_METHOD_GET, "POST": pb.HTTP_METHOD_POST, "PUT": pb.HTTP_METHOD_PUT,
            "DELETE": pb.HTTP_METHOD_DELETE, "PATCH": pb.HTTP_METHOD_PATCH,
            "HEAD": pb.HTTP_METHOD_HEAD, "OPTIONS": pb.HTTP_METHOD_OPTIONS}


@writes.tool(requires_approval=True)
async def create_api(ctx: RunContext[CopilotDeps], project_id: str, method: str, uri: str,
                     headers: dict[str, str] | None = None,
                     params: dict[str, str] | None = None,
                     body: str = "") -> dict:
    """创建 HTTP 接口。method 为大写方法名；body 为原始文本（JSON 字符串）。"""
    api = pb.HttpApi(method=_METHODS.get(method.upper(), pb.HTTP_METHOD_GET), uri=uri)
    for k, v in (headers or {}).items():
        api.headers.add(key=k, value=v)
    for k, v in (params or {}).items():
        api.params.add(key=k, value=v)
    if body:
        api.body.content_type = pb.BODY_CONTENT_TYPE_JSON
        api.body.raw = body
    r = await ctx.deps.sched.stub.CreateApi(
        cpb.CreateApiRequest(ctx=ctx.deps.ctx(), project_id=project_id, http=api))
    return to_dict(r)


@writes.tool(requires_approval=True)
async def create_grpc_api(ctx: RunContext[CopilotDeps], project_id: str,
                          full_service: str, method: str,
                          request_message: dict | None = None,
                          metadata: dict[str, str] | None = None,
                          deadline_ms: int = 0) -> dict:
    """创建 gRPC 接口（执行走 server reflection，无需编译桩）。
    full_service 形如 package.Service；request_message 为 JSON 形态请求体。"""
    g = pb.GrpcApi(full_service=full_service, method=method)
    if request_message:
        from google.protobuf import json_format
        json_format.ParseDict(request_message, g.request_message)
    for k, v in (metadata or {}).items():
        g.metadata.add(key=k, value=v)
    if deadline_ms > 0:
        g.deadline.FromMilliseconds(deadline_ms)
    r = await ctx.deps.sched.stub.CreateApi(
        cpb.CreateApiRequest(ctx=ctx.deps.ctx(), project_id=project_id, grpc=g))
    return to_dict(r)


@writes.tool(requires_approval=True)
async def create_test_case(ctx: RunContext[CopilotDeps], project_id: str, name: str,
                           definition: dict, case_type: str = "declarative",
                           description: str = "") -> dict:
    """创建测试用例。case_type: declarative（definition=DeclarativeCase 的 JSON：{"steps":[...]}）
    或 lowcode（definition={"source": "...", "entry": "run"}）。"""
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
        cpb.CreateTestCaseRequest(ctx=ctx.deps.ctx(), project_id=project_id, case=case))
    return to_dict(r)


def json_format_parse(d: dict, msg) -> None:
    from google.protobuf import json_format
    json_format.ParseDict(d, msg, ignore_unknown_fields=False)


@writes.tool(requires_approval=True)
async def create_test_plan(ctx: RunContext[CopilotDeps], project_id: str, env_id: str,
                           name: str, case_ids: list[str], timeout_ms: int = 300000) -> dict:
    """创建测试计划（按序引用用例）。"""
    plan = pb.TestPlan(name=name, env_id=env_id)
    plan.timeout.FromMilliseconds(timeout_ms)
    for cid in case_ids:
        item = plan.items.add()
        item.case_id = cid
        item.enabled = True
    r = await ctx.deps.sched.stub.CreateTestPlan(
        cpb.CreateTestPlanRequest(ctx=ctx.deps.ctx(), project_id=project_id, plan=plan))
    return to_dict(r)


@writes.tool(requires_approval=True)
async def import_openapi(ctx: RunContext[CopilotDeps], project_id: str,
                         openapi_document: str = "", openapi_url: str = "") -> dict:
    """导入 OpenAPI 3 文档，批量生成接口。二选一：openapi_document（JSON/YAML 原文）
    或 openapi_url（文档 URL，服务端拉取；环回/私网地址会被拒绝）。"""
    req = cpb.ImportOpenApiRequest(ctx=ctx.deps.ctx(), project_id=project_id)
    if openapi_url:
        req.openapi_url = openapi_url
    elif openapi_document:
        req.openapi_document = openapi_document
    else:
        raise ValueError("openapi_document 与 openapi_url 需提供一个")
    r = await ctx.deps.sched.stub.ImportOpenApi(req)
    return to_dict(r)


@writes.tool(requires_approval=True)
async def apply_openapi_diff(ctx: RunContext[CopilotDeps], project_id: str,
                             openapi_document: str, auto_update_cases: bool = False) -> dict:
    """以新 spec 增量更新项目接口（按 method+uri 匹配）：新增→创建、变更→更新、
    缺失→仅报告不删除（kind=breaking 表示参数键被移除或 body content_type 变化）。
    auto_update_cases=true 时，引用变更接口的用例会把新快照内联写回 definition。"""
    r = await ctx.deps.sched.stub.ApplyOpenApiDiff(cpb.ApplyOpenApiDiffRequest(
        ctx=ctx.deps.ctx(), project_id=project_id,
        openapi_document=openapi_document, auto_update_cases=auto_update_cases))
    return to_dict(r)


@writes.tool(requires_approval=True)
async def trigger_run(ctx: RunContext[CopilotDeps], plan_id: str, env_id: str = "") -> dict:
    """触发一次测试计划运行，返回 run_id（可用 get_run 查询结果）。"""
    r = await ctx.deps.sched.stub.TriggerRun(
        cpb.TriggerRunRequest(ctx=ctx.deps.ctx(), plan_id=plan_id, env_id=env_id))
    return to_dict(r)


@writes.tool(requires_approval=True)
async def trigger_stress(ctx: RunContext[CopilotDeps], stress_plan_id: str, env_id: str = "") -> dict:
    """触发一次压测运行。"""
    r = await ctx.deps.sched.stub.TriggerStress(
        cpb.TriggerStressRequest(ctx=ctx.deps.ctx(), stress_plan_id=stress_plan_id, env_id=env_id))
    return to_dict(r)


# parse_struct 占位引用（部分响应含 Struct 时备用）
_ = parse_struct
_ = Any
