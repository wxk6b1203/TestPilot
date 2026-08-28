// Package apperr 定义 REST 层统一错误码体系。
//
// 响应形态：{"error": {"code": "PLAN_NOT_FOUND", "message": "..."}}
// code 稳定、面向调用方；message 面向人；HTTP 状态码由码表决定。
package apperr

import "errors"

// Error 是携带错误码与 HTTP 状态的应用错误。
type Error struct {
	HTTP    int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// ---- 码表 ----
// 命名：<域>_<语义>；通用码无前缀。新增错误必须在此登记并同步 docs/error-codes.md。

const (
	// 通用
	CodeInternal     = "INTERNAL"      // 500 未预期错误
	CodeInvalidJSON  = "INVALID_JSON"  // 400 请求体非法
	CodeInvalidParam = "INVALID_PARAM" // 400 参数缺失/非法
	CodeUnauthorized = "UNAUTHORIZED"  // 401 未认证/凭证无效
	CodeForbidden    = "FORBIDDEN"     // 403 无权限
	CodeNotFound     = "NOT_FOUND"     // 404 资源不存在
	CodeConflict     = "CONFLICT"      // 409 冲突（如重复导入）

	// 认证
	CodeInvalidCredentials   = "AUTH_INVALID_CREDENTIALS" // 401 用户名或密码错误
	CodeNoMembership         = "AUTH_NO_MEMBERSHIP"       // 403 用户不属于任何租户
	CodeRegistrationDisabled = "REGISTRATION_DISABLED"    // 403 注册未开放（配置开关）
	CodeUsernameTaken        = "USERNAME_TAKEN"           // 409 用户名已存在

	// 运行
	CodePlanNotFound = "PLAN_NOT_FOUND"      // 404 计划不存在
	CodeCaseNotFound = "CASE_NOT_FOUND"      // 404 用例不存在
	CodePlanNoItems  = "PLAN_NO_ITEMS"       // 400 计划无启用项
	CodeNoWorker     = "NO_WORKER_AVAILABLE" // 503 无匹配能力的在线 Worker

	// 导入导出
	CodeImportParse     = "IMPORT_PARSE_FAILED" // 400 文档/命令解析失败
	CodeImportDuplicate = "IMPORT_DUPLICATE"    // 409 目标已存在

	// 租户治理（Phase 8）
	CodeAlreadyExists  = "ALREADY_EXISTS"  // 409 目标已存在
	CodeLastOwner      = "LAST_OWNER"      // 409 不能降级/移除最后一名 owner
	CodeQuotaExceeded  = "QUOTA_EXCEEDED"  // 429 配额超限
	CodeDebugTimeout   = "DEBUG_TIMEOUT"   // 504 调试任务超时未回
	CodeOIDCState      = "OIDC_BAD_STATE"  // 400 OIDC state 无效/过期
	CodeOIDCExchange   = "OIDC_EXCHANGE"   // 502 OIDC 令牌交换/验签失败
	CodeIdentityUnlink = "IDENTITY_UNLINK" // 403 OIDC 身份未关联任何用户

	// UI 探测（Copilot gRPC 工具面，docs/ui-probe-design.md §4.9；
	// gRPC 侧用 status.Error 携带这些码前缀，映射见 docs/error-codes.md）
	CodeProbeDisabled     = "PROBE_DISABLED"           // 503 探测功能未开启
	CodeProbeNoWorker     = "PROBE_NO_WORKER"          // 503 无 PLAYWRIGHT 能力在线 Worker
	CodeProbeLimit        = "PROBE_LIMIT"              // 429 会话数超限
	CodeProbeSessionGone  = "PROBE_SESSION_NOT_FOUND"  // 404 会话不存在/已回收
	CodeProbeTimeout      = "PROBE_TIMEOUT"            // 504 探测命令超时
	CodeProbeFailed       = "PROBE_FAILED"             // 500 探测执行失败（Playwright 原文透传）
)

// TooMany 构造 429 错误（配额/限流）。
func TooMany(code, msg string) *Error { return New(429, code, msg) }

// New 构造应用错误。
func New(httpStatus int, code, message string) *Error {
	return &Error{HTTP: httpStatus, Code: code, Message: message}
}

// 常用构造器

func BadRequest(code, msg string) *Error   { return New(400, code, msg) }
func Unauthorized(code, msg string) *Error { return New(401, code, msg) }
func Forbidden(code, msg string) *Error    { return New(403, code, msg) }
func NotFound(code, msg string) *Error     { return New(404, code, msg) }
func Conflict(code, msg string) *Error     { return New(409, code, msg) }
func Internal(msg string) *Error           { return New(500, CodeInternal, msg) }
func Unavailable(code, msg string) *Error  { return New(503, code, msg) }

// From 把任意 error 规整为 *Error（非应用错误归并为 500 INTERNAL）。
func From(err error) *Error {
	var ae *Error
	if errors.As(err, &ae) {
		return ae
	}
	return Internal(err.Error())
}
