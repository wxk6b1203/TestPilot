# testpilot-sdk API 面（低代码脚本 grounding）

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
