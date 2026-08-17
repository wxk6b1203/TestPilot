# TestPilot REST API 参考

> 📚 文档导航：[设计](design.md) · [数据模型](data-model.md) · [路线图](roadmap.md) · [使用指南](usage.md) · [部署](deployment.md) · [API 参考](api.md) · [错误码](error-codes.md) · [v2 特性](v2-features.md)

## 目录

1. 认证 / 租户
2. 领域资源（同构 CRUD）
3. 套件 / 脚本资产（v2）
4. 运行结果
5. 压测
6. 导入导出 / Worker
7. Copilot 反代（不在 /api/v1 下）
8. 定时调度 / 通知 / 配额 / 成员 / 身份源 / 审计
9. Copilot 会话持久化
10. 其他端点（不在 /api/v1 下）

---


基准：`/api/v1`；除 login 与 OIDC 链路外均需 `Authorization: Bearer <token>`。
错误统一 `{"error":{"code","message"}}`，码表见 `docs/error-codes.md`。
**ID 约定**：响应中 id/*_id 为字符串（雪花 ID 超 JS 安全整数）；请求两种形态都收。
**分页**：`?page=&page_size=`（默认 1/20，上限 500）→ `{items, total, page, page_size}`。
**角色**：viewer=4 只读；member=3 领域写；admin=2 租户治理；owner=1 全部（含最后-owner 保护）。

## 认证 / 租户

| 方法 | 路径 | 角色 | 说明 |
|---|---|---|---|
| POST | /auth/login | 公开 | `{username,password}` → `{token,user,tenant_id,role}` |
| POST | /auth/register | 公开* | `{username,password,display_name?,tenant_name?}` → 同 login。注册即自建租户（owner）；*需配置开关 `registration_enabled=true`，否则 403 `REGISTRATION_DISABLED`；用户名重复 409 `USERNAME_TAKEN` |
| GET | /me | viewer | 当前用户/租户/角色 |
| POST | /auth/switch-tenant | viewer | `{tenant_id}` 换签到另一租户（落审计） |
| POST | /tenants | viewer | `{name}` 自助建租户，创建者为 owner |
| GET | /auth/oidc/providers | 公开 | 启用的 OIDC 身份源列表 |
| GET | /auth/oidc/{id}/login | 公开 | 302 跳 IdP |
| GET | /auth/oidc/{id}/callback | 公开 | 回调 → `{token,user,tenant_id,role}` |

## 领域资源（projects/environments/variables/certificates/apis/cases/plans 同构）

| 方法 | 路径 | 角色 | 说明 |
|---|---|---|---|
| GET | /projects · /environments · /variables · /certificates · /apis · /cases · /plans | viewer | 分页列表；支持 `?project_id=` 过滤（variables 另有 `?environment_id=`） |
| POST | 同上 | member | 创建（plans 体含 `items[]`，item 需 `ref_type:1` + `ref_id`） |
| GET/PUT/DELETE | `/{资源}/{id}` | GET viewer / 写 member | 单资源操作 |
| GET | `/projects/{id}/api-wrappers` | viewer | 低代码接口封装预览；`?http_ids=1,2&grpc_ids=3` 过滤，缺省=项目全部接口；`?format=py`（默认，执行文件）或 `format=stub`（自包含 `.pyi` 补全 stub） |

- `GET /variables` 命中 sensitive 行时落 `secret_read` 审计。
- `/certificates`：`{project_id,name,type(pem|p12),cert_ref,key_ref,password_secret_ref?}`；当前为资产 CRUD，Worker 客户端证书执行另议。
- `POST /plans/{id}/run`（member）：触发运行 → `{run_id}`；配额超限 → 429。
- 接口 `cookies`/`settings.tls_verify`/`settings.comment_tolerant_json`/`body.binary_ref` 已进入执行引擎，语义见 `docs/usage.md`。

## gRPC 接口 / proto 文件（v2 第三批）

| 方法 | 路径 | 角色 | 说明 |
|---|---|---|---|
| GET/POST | /grpc-apis | viewer/member | `{project_id, full_service, method, request_message?, metadata?, deadline_ms?, tls_settings?, proto_ref?}` |
| GET/PUT/DELETE | /grpc-apis/{id} | viewer/member | 执行走 server reflection；目标地址取环境 base_url（host[:port]） |
| GET/POST | /proto-files | viewer/member | `{project_id, filename, content, imports?}`——proto 源文件资产（内联存储） |
| GET/DELETE | /proto-files/{id} | viewer/member | 内容随行返回 |

- 用例步骤 `grpc_call: {"grpc_api_id": "<id>", "request_override": {...}, "metadata_override": [...]}`：
  派发前解析进任务级 `grpc_apis` 映射；响应经 `$.字段` JSONPATH 断言（HTTP 与 gRPC 同断言体系）。

## 套件 / 脚本资产（v2）

| 方法 | 路径 | 角色 | 说明 |
|---|---|---|---|
| GET/POST | /suites | viewer/member | `{name, description, project_id, case_ids[]}`；case_ids 有序（order=下标） |
| GET/PUT/DELETE | /suites/{id} | viewer/member | 更新时 items 全量替换；删除连带 items |
| GET/POST | /scripts | viewer/member | `{name, project_id, language, content}`；content 必填，language 默认 python |
| GET/PUT/DELETE | /scripts/{id} | viewer/member | 低代码用例 `definition.script_ref` 引用目标 |

- 计划 item `ref_type=2` + `ref_id=<suite id>`：触发时按套件 items 顺序展开派发
  （详见 `docs/v2-features.md`）。

## 运行结果

| 方法 | 路径 | 角色 | 说明 |
|---|---|---|---|
| GET | /runs | viewer | `?plan_id=` 过滤 |
| GET | /runs/{id} | viewer | run + cases + steps（含请求/响应快照/断言明细） |
| POST | /runs/{id}/cancel | member | 取消 RUNNING 运行：run→ABORTED、未决 case→SKIPPED、广播 Worker cancel |
| GET | /artifacts/{id}/content | viewer | 产物文件内容（截图/trace/har） |

## 压测

| 方法 | 路径 | 角色 | 说明 |
|---|---|---|---|
| GET/POST | /stress-plans | viewer/member | load_profile JSON（concurrency/duration/ramp）；`target_type:1`=接口 / `target_type:2`=低代码行为用例 |
| GET/PUT/DELETE | /stress-plans/{id} | viewer/member | |
| POST | /stress-plans/{id}/run | member | → `{run_id}` |
| GET | /stress-runs · /stress-runs/{id} | viewer | 详情含时序指标点 |

## 导入导出 / Worker

| 方法 | 路径 | 角色 | 说明 |
|---|---|---|---|
| POST | /import/openapi | member | OpenAPI 3 JSON/YAML（幂等跳过重复） |
| POST | /import/curl | member | curl 命令文本 → 接口 |
| POST | /import/postman | member | Postman Collection v2.1 JSON（folder 递归、query/header/body 映射、幂等） |
| GET | /export/openapi?project_id= | viewer | 导出 OpenAPI 3 |
| GET | /export/curl?project_id= | viewer | 导出 curl 命令脚本（每接口一行） |
| GET | /export/postman?project_id= | viewer | 导出 Postman Collection v2.1（`{{base_url}}` 占位） |
| GET | /workers | viewer | 在线 Worker（能力/负载/标签） |

## Copilot 反代（不在 /api/v1 下）

| 方法 | 路径 | 角色 | 说明 |
|---|---|---|---|
| ALL | /copilot-api/* | Copilot 侧校验 | 反向代理到 `TP_COPILOT_URL`（路径重写为 /api/*；SSE 逐 token flush；上游不可达 502 `COPILOT_UNAVAILABLE`） |

## 定时调度 / 通知 / 配额 / 成员 / 身份源 / 审计

| 方法 | 路径 | 角色 | 说明 |
|---|---|---|---|
| GET/POST | /schedules | viewer/member | `{plan_id,env_id,cron_expr,overlap_policy,enabled}` |
| PUT/DELETE | /schedules/{id} | member | |
| GET/POST | /notifications | admin | type 1=webhook 2=dingtalk 3=feishu；events 逗号分隔 |
| PUT/DELETE | /notifications/{id} | admin | |
| GET | /tenant/quotas | admin | 全部 metric 的 limit+used |
| PUT | /tenant/quotas/{metric} | admin | `{limit}`；≤0 删除（=不限） |
| GET | /tenant/settings | admin | 租户配置开关列表（key-value） |
| PUT | /tenant/settings/{key} | admin | `{value}` upsert；key 限 `[A-Za-z0-9_.-]{1,64}` |
| DELETE | /tenant/settings/{key} | admin | 删除（不存在 404） |
| GET/POST | /tenant/members | admin | addMember 可按需创建用户（默认密码 changeme123） |
| PUT/DELETE | /tenant/members/{userID} | admin | 最后 owner 不可降级/移除（409 LAST_OWNER） |
| GET/POST | /identity-providers | admin | OIDC/OAuth2 身份源（issuer/client_id/client_secret + `type: oidc|oauth2` + 可选 authorization/token/userinfo_endpoint 覆盖） |
| PUT/DELETE | /identity-providers/{id} | admin | |
| GET | /audit-logs | admin | actor 1=human 2=copilot |

## Copilot 会话持久化

| 方法 | 路径 | 角色 | 说明 |
|---|---|---|---|
| GET/POST | /copilot/sessions | viewer/member | |
| GET/POST | /copilot/sessions/{id}/messages | viewer/member | role 1=user 2=assistant 3=tool |

## 其他端点（不在 /api/v1 下）

- `GET /healthz`：存活探针。
- `GET /metrics`：Prometheus 指标（公开，生产收敛到内网）。
- Copilot 服务（:8100）：`POST /api/chat`（Vercel AI SSE；头 `X-Session-Id` 续会话）、`GET /api/healthz`。
