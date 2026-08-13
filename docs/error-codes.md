# REST 错误码体系

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

## 约定

1. 新增错误码必须在 `scheduler/internal/apperr/apperr.go` 登记并同步本表。
2. Handler 用 `writeAppErr(w, apperr.NotFound(apperr.CodeXxx, "..."))`；未知 error 自动归并为 `INTERNAL`。
3. 前端/CLI 按 `code` 做程序化判断（如 `NO_WORKER_AVAILABLE` 提示"请先启动 Worker"）。
