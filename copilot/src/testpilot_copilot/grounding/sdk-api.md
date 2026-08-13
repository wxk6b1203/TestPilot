# testpilot-sdk API 面（低代码脚本 grounding）

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
