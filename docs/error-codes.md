# REST 错误码体系

> 📚 文档导航：[设计](design.md) · [数据模型](data-model.md) · [路线图](roadmap.md) · [使用指南](usage.md) · [部署](deployment.md) · [API 参考](api.md) · [错误码](error-codes.md) · [工程化](ci-migration-plan.md) · [低代码 ID 调用](lowcode-api-invocation.md) · [技术博文](blog-lowcode-copilot.md) · [v2 特性存档](v2-features.md)

## 目录

1. 码表
2. 约定

---


所有 `/api/v1` 错误响应统一形态：

```json
{ "error": { "code": "PLAN_NOT_FOUND", "message": "plan not found" } }
```

- `code`：稳定、面向调用方（程序可按 code 分支处理）；**只增不改**。
- `message`：面向人，可随时调整，不要依赖其内容做逻辑判断。
- HTTP 状态码由错误码决定（见下表）。

## 码表

| code | HTTP | 含义 | 常见触发 |
|---|---|---|---|
| `INTERNAL` | 500 | 未预期服务端错误 | DB 故障等 |
| `INVALID_JSON` | 400 | 请求体不是合法 JSON | decode 失败 |
| `INVALID_PARAM` | 400 | 参数缺失/非法 | path/query/body 校验失败 |
| `UNAUTHORIZED` | 401 | 未认证或 token 无效 | 缺/错 Bearer token |
| `FORBIDDEN` | 403 | 已认证但无权限 | 角色不足 |
| `NOT_FOUND` | 404 | 资源不存在（或不在本租户） | GET/PUT/DELETE 未命中 |
| `CONFLICT` | 409 | 冲突 | 重复创建 |
| `AUTH_INVALID_CREDENTIALS` | 401 | 用户名或密码错误 | login |
| `AUTH_NO_MEMBERSHIP` | 403 | 用户不属于任何租户 | login |
| `REGISTRATION_DISABLED` | 403 | 公开注册被配置关闭 | `registration_enabled=false` |
| `USERNAME_TAKEN` | 409 | 用户名已存在 | 重复注册 |
| `PLAN_NOT_FOUND` | 404 | 测试计划不存在 | 触发运行 |
| `PLAN_NO_ITEMS` | 400 | 计划无启用项 | 触发运行 |
| `NO_WORKER_AVAILABLE` | 503 | 无匹配能力的在线 Worker | 触发运行/压测 |
| `IMPORT_PARSE_FAILED` | 400 | OpenAPI/curl 文档解析失败 | 导入 |
| `IMPORT_DUPLICATE` | 409 | 导入目标已存在 | curl 导入重复接口 |
| `ALREADY_EXISTS` | 409 | 目标已存在 | 重复添加租户成员 |
| `LAST_OWNER` | 409 | 不能降级/移除最后一名 owner | 成员管理 |
| `QUOTA_EXCEEDED` | 429 | 租户配额超限 | 触发运行/AI 调用/Worker 注册/产物上传 |
| `OIDC_BAD_STATE` | 400 | OIDC state 无效/过期（>10min 或重放） | OIDC 回调 |
| `OIDC_EXCHANGE` | 502 | OIDC 令牌交换/验签失败 | OIDC 回调 |
| `IDENTITY_UNLINK` | 403 | OIDC 身份未关联任何用户 | OIDC 回调 |

### gRPC Copilot 工具面（UI 探测，v1）

UI 探测 RPC（OpenProbe/ActProbe/EvalProbe 等）走 gRPC `status.Error`，`message` 以下列码为前缀（前端/CLI 按前缀分支）：

| code 前缀 | gRPC code | 含义 |
|---|---|---|
| `PROBE_DISABLED` | FailedPrecondition | 探测功能未开启（probe_enabled） |
| `PROBE_NO_WORKER` | Unavailable | 无 PLAYWRIGHT 能力在线 Worker |
| `PROBE_LIMIT` | ResourceExhausted | 探测会话数超限 |
| `PROBE_SESSION_NOT_FOUND` | NotFound | 会话不存在/已回收（TTL/断连） |
| `PROBE_TIMEOUT` | DeadlineExceeded | 探测命令执行超时 |
| `PROBE_FAILED` | Internal | 探测执行失败（Playwright 报错原文透传） |

## 约定

1. 新增错误码必须在 `scheduler/internal/apperr/apperr.go` 登记并同步本表。
2. Handler 用 `writeAppErr(w, apperr.NotFound(apperr.CodeXxx, "..."))`；未知 error 自动归并为 `INTERNAL`。
3. 前端/CLI 按 `code` 做程序化判断（如 `NO_WORKER_AVAILABLE` 提示"请先启动 Worker"）。
