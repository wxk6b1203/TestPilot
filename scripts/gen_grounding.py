#!/usr/bin/env python3
"""构建期 grounding 生成器（design.md §7.4）。

从 proto/testpilot/common/v1/types.proto 提取领域 schema（message/enum + 行注释），
从 worker/src/testpilot_sdk 提取低代码 SDK API 面，产出：
  - scheduler/internal/grpcserver/schema.json   （QuerySchema 工具服务，go:embed）
  - copilot/src/testpilot_copilot/grounding/domain-schema.json
  - copilot/src/testpilot_copilot/grounding/sdk-api.md

用法：worker/.venv/bin/python scripts/gen_grounding.py
"""

from __future__ import annotations

import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
PROTO = ROOT / "proto/testpilot/common/v1/types.proto"
SDK = ROOT / "worker/src/testpilot_sdk"

# Copilot 关心的核心领域对象（全量 proto 太大，噪音稀释 grounding 效果）
CORE_MESSAGES = [
    "HttpApi", "KeyValue", "BodySpec", "Assertion", "TestStep", "ApiCallStep",
    "AssertionStep", "SetVarStep", "IfStep", "LoopStep", "RetryStep", "DelayStep",
    "CodeBlockStep", "UiActionStep", "DeclarativeCase", "LowCodeCase", "TestCase",
    "TestPlan", "PlanItem", "Environment", "Variable", "TestRun", "TestCaseResult",
    "TestStepResult", "StressTestPlan", "LoadProfile", "RampStage",
]
CORE_ENUMS = [
    "HttpMethod", "StepType", "UiAction", "AssertionTarget", "AssertionOp",
    "TestCaseType", "RunStatus", "CaseStatus", "StepStatus",
]

MSG_RE = re.compile(r"^message (\w+) \{", re.M)
ENUM_RE = re.compile(r"^enum (\w+) \{", re.M)
FIELD_RE = re.compile(
    r"^\s*(?:repeated\s+|optional\s+)?([\w.]+)\s+(\w+)\s*=\s*\d+[^/]*(?://\s*(.*))?$")


def extract() -> dict:
    text = PROTO.read_text(encoding="utf-8")
    out = {"version": "v1", "source": "proto/testpilot/common/v1/types.proto",
           "messages": {}, "enums": {}}

    blocks: dict[str, str] = {}
    for m in MSG_RE.finditer(text):
        name, start = m.group(1), m.end()
        depth, i = 1, start
        while depth and i < len(text):
            depth += {"{": 1, "}": -1}.get(text[i], 0)
            i += 1
        blocks[name] = text[start:i - 1]
    for m in ENUM_RE.finditer(text):
        name, start = m.group(1), m.end()
        depth, i = 1, start
        while depth and i < len(text):
            depth += {"{": 1, "}": -1}.get(text[i], 0)
            i += 1
        blocks.setdefault(name, text[start:i - 1])

    for name in CORE_MESSAGES:
        body = blocks.get(name)
        if body is None:
            continue
        fields = []
        for line in body.splitlines():
            fm = FIELD_RE.match(line)
            if fm and not line.strip().startswith(("oneof", "message", "enum")):
                typ, fname, doc = fm.group(1), fm.group(2), (fm.group(3) or "").strip()
                prefix = "repeated " if line.strip().startswith("repeated") else ""
                fields.append({"name": fname, "type": prefix + typ, **({"doc": doc} if doc else {})})
            elif line.strip().startswith("oneof"):
                oneof = line.strip().rstrip(" {")
                fields.append({"name": oneof.split()[-1], "type": "oneof", "doc": "见 proto"})
        out["messages"][name] = fields
    for name in CORE_ENUMS:
        body = blocks.get(name)
        if body is None:
            continue
        values = re.findall(r"^\s*(\w+)\s*=\s*(\d+)", body, re.M)
        out["enums"][name] = {v: int(n) for v, n in values}
    return out


SDK_API_MD = """# testpilot-sdk API 面（低代码脚本 grounding）

沙箱内可用 import：`from testpilot_sdk import Context, HttpAPI, GrpcAPI, Response, GrpcResponse, assert_that`

## Context（入口函数参数）
- `async def run(ctx: Context)` — 入口签名；ctx 注入全部能力
- `ctx.vars` — 变量视图：读 `ctx.vars["k"]`、写 `ctx.vars["k"] = v`（运行结束合并回用例上下文）
- `ctx.base_url: str` / `ctx.parameters: dict` / `ctx.tenant_id: int`
- `ctx.http_api(api_id) -> HttpAPI` / `ctx.grpc_api(api_id) -> GrpcAPI` / `ctx.api(api_id)`
  — 按接口目录 ID 调用（推荐）；api_id 必须声明在 definition.httpApiRefs / grpcApiRefs 中
- `await ctx.http(method, uri, *, headers=None, params=None, body=None, timeout=30.0) -> Response`
  — 底层逃生通道：目录外/临时请求；相对 uri 自动拼 base_url
- `await ctx.set_var(key, value)` — 显式回写变量
- `ctx.log(message)` — 写入步骤日志

## HttpAPI（按 ID / 生成封装 / raw 兼容）
```python
# 1) 按 ID（推荐，接口目录是唯一真相源）
resp = await ctx.http_api("123").run(body={"u": "neo"}, headers={"X-Trace": "t"})
# 2) 自动生成封装（派发时生成 tp_api_wrappers.py）
from tp_api_wrappers import Api123, CreateUser   # Api123 是稳定类名
resp = await CreateUser(body={"u": "neo"}).run()
# 3) raw 兼容：HttpAPI(method="POST", uri="/login").run() == ctx.http
```
- run 的 kwargs 与显式实例字段只作为 override：method/uri 整体替换，headers/params/cookies
  按键合并，body 整体替换（dict/list→JSON，str→原文，binary_ref→二进制引用）；
- tls_verify / follow_redirects 等安全设置以接口快照为准，脚本不可覆盖；
- 接口级 cookies / 证书 / JSONC / binary_ref / pre-post 脚本与声明式 api_call 语义一致。

## GrpcAPI（按 ID / raw）
```python
g = await ctx.grpc_api("456").run(request={"message": "hi"}, metadata={"trace": "x"})
from tp_api_wrappers import Api456
g = await Api456().run(request={"message": "hi"})   # request 深合并快照
# raw：GrpcAPI(full_service="pkg.Svc", method="M").run(request={...})
```

## Response（pydantic 模型）
- HTTP：`status: int` / `headers: dict` / `body: Any`（JSON 解析后）/ `text: str` /
  `elapsed_ms: int` / `api_id: str` / `request: dict`
- gRPC：`GrpcResponse.status` / `.json`（返回体，等价声明式 $.json）/ `.request` / `.elapsed_ms`

## assert_that(actual, label="") — 链式断言（失败即 fail-fast，结果入报告）
`.eq(v) .ne(v) .gt(n) .ge(n) .lt(n) .le(n) .contains(x) .matches(regex) .exists() .type_is("object|array|string|number|boolean|null")`

## 接口依赖声明（LowCodeCase）
- definition.httpApiRefs / grpcApiRefs 显式声明依赖；脚本中的字面量 ID 会在派发时被静态提取；
- 动态拼接 ID（getattr/f-string）必须显式声明，否则运行时报未声明错误；
- `from tp_api_wrappers import ...` 且无任何 refs 时，Scheduler 会兜底包含本项目全部接口（上限 200）。

## 约束
- 沙箱无网络（禁止 requests/httpx/socket 直连）、无环境变量（白名单）、CPU/内存受限
- 副作用只能经 ctx（HTTP / gRPC / 变量 / 日志）；文件写仅限临时目录
"""



def main() -> None:
    schema = extract()
    targets = [
        ROOT / "scheduler/internal/grpcserver/schema.json",
        ROOT / "copilot/src/testpilot_copilot/grounding/domain-schema.json",
    ]
    payload = json.dumps(schema, ensure_ascii=False, indent=1)
    for t in targets:
        t.parent.mkdir(parents=True, exist_ok=True)
        t.write_text(payload, encoding="utf-8")
    sdk_md = ROOT / "copilot/src/testpilot_copilot/grounding/sdk-api.md"
    sdk_md.parent.mkdir(parents=True, exist_ok=True)
    sdk_md.write_text(SDK_API_MD, encoding="utf-8")
    print(f"grounding: {len(schema['messages'])} messages, {len(schema['enums'])} enums")
    for t in [*targets, sdk_md]:
        print("  wrote", t.relative_to(ROOT))


if __name__ == "__main__":
    main()
