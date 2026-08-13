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

沙箱内可用 import：`from testpilot_sdk import Context, HttpAPI, Response, assert_that`

## Context（入口函数参数）
- `async def run(ctx: Context)` — 入口签名；ctx 注入全部能力
- `ctx.vars` — 变量视图：`ctx.vars["k"]` 读、`ctx.vars["k"] = v` 写（运行结束合并回用例上下文）
- `ctx.base_url: str` / `ctx.parameters: dict` / `ctx.tenant_id: int`
- `await ctx.http(method, uri, *, headers=None, params=None, body=None, timeout=30.0) -> Response`
  — 经能力桥由 Worker 代执行；uri 相对路径自动拼 base_url
- `await ctx.set_var(key, value)` — 显式回写变量
- `ctx.log(message)` — 写入步骤日志

## HttpAPI（声明式风格）
```python
api = HttpAPI(method="POST", uri="/login", body={"u": "neo"})
resp = await api.run()           # 等价于 ctx.http
```

## Response（pydantic 模型）
- `status: int` / `headers: dict` / `body: Any`（JSON 解析后）/ `text: str` / `elapsed_ms: int`

## assert_that(actual, label="") — 链式断言（失败即 fail-fast，结果入报告）
`.eq(v) .ne(v) .gt(n) .ge(n) .lt(n) .le(n) .contains(x) .matches(regex) .exists() .type_is("object|array|string|number|boolean|null")`

## 约束
- 沙箱无网络（禁止 requests/httpx/socket 直连）、无环境变量（白名单）、CPU/内存受限
- 副作用只能经 ctx（HTTP / 变量 / 日志）；文件写仅限临时目录
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
