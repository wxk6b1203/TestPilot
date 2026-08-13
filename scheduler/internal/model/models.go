package model

import (
	"database/sql/driver"
	"fmt"
	"sync"
	"time"

	"gorm.io/gorm"
)

// 枚举与 proto testpilot.common.v1 对齐（编号一致）。

// ---- 雪花 ID（snowflake）生成：41bit 毫秒时间戳 | 10bit 节点 | 12bit 序列 ----
var (
	idMu     sync.Mutex
	idNode   int64 = 1
	idLastMs int64
	idSeq    int64
	idEpoch  int64 = 1704067200000 // 2024-01-01 UTC ms
)

// NextID 生成趋势递增的 int64 主键。
func NextID() int64 {
	idMu.Lock()
	defer idMu.Unlock()
	now := time.Now().UnixMilli()
	if now == idLastMs {
		idSeq = (idSeq + 1) & 0xFFF
		if idSeq == 0 {
			for now <= idLastMs {
				now = time.Now().UnixMilli()
			}
		}
	} else {
		idSeq = 0
	}
	idLastMs = now
	return ((now - idEpoch) << 22) | (idNode << 12) | idSeq
}

// JSON 是 JSONB/TEXT 列的通用载体（存 proto 实体的 JSON 表示）。
// 实现 Scanner/Valuer，避免 TEXT 列 Scan 到 []byte 命名类型失败。
type JSON []byte

func (j *JSON) Scan(v any) error {
	switch x := v.(type) {
	case nil:
		*j = nil
		return nil
	case string:
		*j = append((*j)[:0], x...)
		return nil
	case []byte:
		*j = append((*j)[:0], x...)
		return nil
	}
	return fmt.Errorf("model.JSON: unsupported scan type %T", v)
}

func (j JSON) Value() (driver.Value, error) {
	if len(j) == 0 {
		return nil, nil
	}
	return string(j), nil
}

func (j JSON) MarshalJSON() ([]byte, error) {
	if len(j) == 0 {
		return []byte("null"), nil
	}
	return j, nil
}

func (j *JSON) UnmarshalJSON(b []byte) error {
	*j = append((*j)[:0], b...)
	return nil
}

// ---- 租户与访问控制 ----

type Tenant struct {
	ID        int64     `json:"id" gorm:"primaryKey"`
	Name      string    `json:"name"`
	Status    int16     `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type User struct {
	ID           int64     `json:"id" gorm:"primaryKey"`
	Username     string    `json:"username" gorm:"uniqueIndex"`
	Email        string    `json:"email" gorm:"index"`
	PasswordHash string    `json:"-"`
	DisplayName  string    `json:"display_name"`
	Status       int16     `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
}

type TenantMember struct {
	ID        int64     `json:"id" gorm:"primaryKey"`
	TenantID  int64     `json:"tenant_id" gorm:"uniqueIndex:idx_tm_tenant_user"`
	UserID    int64     `json:"user_id" gorm:"uniqueIndex:idx_tm_tenant_user"`
	Role      int16     `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

// ---- 项目 / 环境 / 变量 / 证书 ----

type Project struct {
	ID          int64          `json:"id" gorm:"primaryKey"`
	TenantID    int64          `json:"tenant_id" gorm:"index"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Config      JSON           `json:"config,omitempty" gorm:"type:text"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `json:"-" gorm:"index"`
}

type Environment struct {
	ID          int64  `json:"id" gorm:"primaryKey"`
	TenantID    int64  `json:"tenant_id" gorm:"index:idx_env_tp"`
	ProjectID   int64  `json:"project_id" gorm:"index:idx_env_tp"`
	Icon        string `json:"icon"`
	Name        string `json:"name"`
	Description string `json:"description"`
	BaseURL     string `json:"base_url"`
}

type Variable struct {
	ID            int64  `json:"id" gorm:"primaryKey"`
	TenantID      int64  `json:"tenant_id" gorm:"index"`
	ProjectID     int64  `json:"project_id" gorm:"index"`
	EnvironmentID int64  `json:"environment_id" gorm:"index"` // 0 = 项目级
	Scope         int16  `json:"scope"`
	Category      int16  `json:"category"`
	Key           string `json:"key"`
	Value         string `json:"value"`
	Sensitive     bool   `json:"sensitive"`
	SecretRef     string `json:"secret_ref"`
	Description   string `json:"description"`
}

type Certificate struct {
	ID                int64  `json:"id" gorm:"primaryKey"`
	TenantID          int64  `json:"tenant_id" gorm:"index"`
	ProjectID         int64  `json:"project_id" gorm:"index"`
	Name              string `json:"name"`
	Description       string `json:"description"`
	Type              string `json:"type"`
	CertRef           string `json:"cert_ref"`
	KeyRef            string `json:"key_ref"`
	PasswordSecretRef string `json:"password_secret_ref"`
}

// ---- 接口 ----

type HttpApi struct {
	ID            int64          `json:"id" gorm:"primaryKey"`
	TenantID      int64          `json:"tenant_id" gorm:"index:idx_http_tp"`
	ProjectID     int64          `json:"project_id" gorm:"index:idx_http_tp"`
	Method        int16          `json:"method"`
	URI           string         `json:"uri"`
	Params        JSON           `json:"params,omitempty" gorm:"type:text"`
	Body          JSON           `json:"body,omitempty" gorm:"type:text"`
	Headers       JSON           `json:"headers,omitempty" gorm:"type:text"`
	Cookies       JSON           `json:"cookies,omitempty" gorm:"type:text"`
	PreScripts    JSON           `json:"pre_scripts,omitempty" gorm:"type:text"`
	PostScripts   JSON           `json:"post_scripts,omitempty" gorm:"type:text"`
	Settings      JSON           `json:"settings,omitempty" gorm:"type:text"`
	CertificateID int64          `json:"certificate_id"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	DeletedAt     gorm.DeletedAt `json:"-" gorm:"index"`
}

// ---- 目录树 ----

type TreeNode struct {
	ID        int64  `json:"id" gorm:"primaryKey"`
	TenantID  int64  `json:"tenant_id" gorm:"index:idx_tree_tp"`
	ProjectID int64  `json:"project_id" gorm:"index:idx_tree_tp"`
	ParentID  int64  `json:"parent_id"`
	NodeType  int16  `json:"node_type"`
	RefID     int64  `json:"ref_id"`
	Name      string `json:"name"`
	Icon      string `json:"icon"`
	Order     int    `json:"order"`
	Path      string `json:"path" gorm:"index"`
}

// ---- 测试用例 / 计划 ----

type TestCase struct {
	ID          int64          `json:"id" gorm:"primaryKey"`
	TenantID    int64          `json:"tenant_id" gorm:"index:idx_case_tp"`
	ProjectID   int64          `json:"project_id" gorm:"index:idx_case_tp"`
	Type        int16          `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Definition  JSON           `json:"definition,omitempty" gorm:"type:text"`
	Tags        JSON           `json:"tags,omitempty" gorm:"type:text"`
	CreatedBy   int16          `json:"created_by"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `json:"-" gorm:"index"`
}

type TestPlan struct {
	ID             int64          `json:"id" gorm:"primaryKey"`
	TenantID       int64          `json:"tenant_id" gorm:"index"`
	ProjectID      int64          `json:"project_id" gorm:"index"`
	EnvID          int64          `json:"env_id"`
	Name           string         `json:"name"`
	Concurrency    int            `json:"concurrency"`
	RetryOnFailure bool           `json:"retry_on_failure"`
	OverlapPolicy  int16          `json:"overlap_policy"`
	ScheduleCron   string         `json:"schedule_cron"`
	TimeoutMs      int            `json:"timeout_ms"`
	Notifications  JSON           `json:"notifications,omitempty" gorm:"type:text"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	DeletedAt      gorm.DeletedAt `json:"-" gorm:"index"`
}

type TestPlanItem struct {
	ID             int64 `json:"id" gorm:"primaryKey"`
	TenantID       int64 `json:"tenant_id"`
	PlanID         int64 `json:"plan_id" gorm:"index"`
	RefType        int16 `json:"ref_type"` // 1=case 2=suite
	RefID          int64 `json:"ref_id"`
	Enabled        bool  `json:"enabled"`
	ParamOverrides JSON  `json:"param_overrides,omitempty" gorm:"type:text"`
	Order          int   `json:"order"`
}

// ---- 运行结果 ----

type TestRun struct {
	ID          int64      `json:"id" gorm:"primaryKey"`
	TenantID    int64      `json:"tenant_id" gorm:"index:idx_run_tp"`
	PlanID      int64      `json:"plan_id" gorm:"index:idx_run_tp"`
	EnvID       int64      `json:"env_id"`
	Status      int16      `json:"status" gorm:"index"`
	Trigger     int16      `json:"trigger"`
	TriggeredBy string     `json:"triggered_by"`
	Summary     JSON       `json:"summary,omitempty" gorm:"type:text"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at"`
}

type TestCaseResult struct {
	ID         int64  `json:"id" gorm:"primaryKey"`
	TenantID   int64  `json:"tenant_id" gorm:"index"`
	RunID      int64  `json:"run_id" gorm:"index"`
	CaseID     int64  `json:"case_id"`
	Status     int16  `json:"status"`
	DurationMs int    `json:"duration_ms"`
	Error      string `json:"error"`
}

type TestStepResult struct {
	ID           int64  `json:"id" gorm:"primaryKey"`
	TenantID     int64  `json:"tenant_id" gorm:"index"`
	CaseResultID int64  `json:"case_result_id" gorm:"index"`
	StepPath     string `json:"step_path"`
	Status       int16  `json:"status"`
	DurationMs   int    `json:"duration_ms"`
	Request      JSON   `json:"request,omitempty" gorm:"type:text"`
	Response     JSON   `json:"response,omitempty" gorm:"type:text"`
	Assertions   JSON   `json:"assertions,omitempty" gorm:"type:text"`
	Logs         JSON   `json:"logs,omitempty" gorm:"type:text"`
}

// Artifact 产物（截图/trace/har/下载文件等）。Kind 取值见 ArtifactKind* 常量。
type Artifact struct {
	ID           int64  `json:"id" gorm:"primaryKey"`
	TenantID     int64  `json:"tenant_id" gorm:"index"`
	RunID        int64  `json:"run_id" gorm:"index"`
	StepResultID int64  `json:"step_result_id" gorm:"index"`
	Kind         int16  `json:"kind"`
	URI          string `json:"uri"`
	Size         int64  `json:"size"`
	CreatedAt    int64  `json:"created_at" gorm:"autoCreateTime:milli"`
}

// Artifact.Kind 取值（与 docs/sql 注释一致）。
const (
	ArtifactKindScreenshot int16 = 1
	ArtifactKindVideo      int16 = 2
	ArtifactKindTrace      int16 = 3
	ArtifactKindHar        int16 = 4
	ArtifactKindDownload   int16 = 5
	ArtifactKindLog        int16 = 6
	ArtifactKindProto      int16 = 7
	ArtifactKindCert       int16 = 8
)

// ---- 压力测试 ----

// StressTestPlan 压测计划（target_type: 1=api 2=behavior_case）。
type StressTestPlan struct {
	ID                int64          `json:"id" gorm:"primaryKey"`
	TenantID          int64          `json:"tenant_id" gorm:"index"`
	ProjectID         int64          `json:"project_id" gorm:"index"`
	EnvID             int64          `json:"env_id"`
	TargetType        int16          `json:"target_type"`
	TargetID          int64          `json:"target_id"`
	LoadProfile       JSON           `json:"load_profile" gorm:"type:text"`
	WorkerCount       int            `json:"worker_count"`
	MetricsIntervalMs int            `json:"metrics_interval_ms"`
	ScheduleCron      string         `json:"schedule_cron"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
	DeletedAt         gorm.DeletedAt `json:"-" gorm:"index"`
}

// StressRun 一次压测运行。Status 复用 RunStatus（0=PENDING 1=RUNNING 2=PASSED 3=FAILED 4=CANCELED）。
type StressRun struct {
	ID           int64      `json:"id" gorm:"primaryKey"`
	TenantID     int64      `json:"tenant_id" gorm:"index"`
	StressPlanID int64      `json:"stress_plan_id" gorm:"index"`
	EnvID        int64      `json:"env_id"`
	Status       int16      `json:"status" gorm:"index"`
	Summary      JSON       `json:"summary,omitempty" gorm:"type:text"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
}

// StressMetricPoint 压测时序指标点（dev 内嵌存储；生产换 VictoriaMetrics，查询层不变）。
type StressMetricPoint struct {
	ID           int64     `json:"id" gorm:"primaryKey"`
	TenantID     int64     `json:"tenant_id" gorm:"index"`
	StressRunID  int64     `json:"stress_run_id" gorm:"index"`
	Ts           time.Time `json:"ts"`
	Rps          float64   `json:"rps"`
	LatencyP50Ms float64   `json:"latency_p50_ms"`
	LatencyP95Ms float64   `json:"latency_p95_ms"`
	LatencyP99Ms float64   `json:"latency_p99_ms"`
	ErrorRate    float64   `json:"error_rate"`
	Concurrency  int       `json:"concurrency"`
}

// ---- Copilot / 审计 ----

type CopilotSession struct {
	ID        int64     `json:"id" gorm:"primaryKey"`
	TenantID  int64     `json:"tenant_id" gorm:"index"`
	UserID    int64     `json:"user_id" gorm:"index"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CopilotMessage 会话消息（role: 1=user 2=assistant 3=tool）。
type CopilotMessage struct {
	ID        int64     `json:"id" gorm:"primaryKey"`
	TenantID  int64     `json:"tenant_id"`
	SessionID int64     `json:"session_id" gorm:"index"`
	Role      int16     `json:"role"`
	Content   string    `json:"content" gorm:"type:text"`
	ToolCalls JSON      `json:"tool_calls,omitempty" gorm:"type:text"`
	CreatedAt time.Time `json:"created_at"`
}

// AuditLog 审计（actor: 1=human 2=copilot）。
type AuditLog struct {
	ID           int64     `json:"id" gorm:"primaryKey"`
	TenantID     int64     `json:"tenant_id" gorm:"index"`
	Actor        int16     `json:"actor"`
	ActorID      string    `json:"actor_id"`
	Action       string    `json:"action"`
	ResourceType string    `json:"resource_type"`
	ResourceID   string    `json:"resource_id"`
	ApprovedBy   string    `json:"approved_by"`
	Detail       JSON      `json:"detail,omitempty" gorm:"type:text"`
	CreatedAt    time.Time `json:"created_at"`
}

// Schedule 定时调度（cron 触发 TestPlan 运行）。
// OverlapPolicy: 1=跳过（上次未结束） 2=允许并发。NextRunAt 用于 misfire 检测。
type Schedule struct {
	ID            int64      `json:"id" gorm:"primaryKey"`
	TenantID      int64      `json:"tenant_id" gorm:"index"`
	PlanID        int64      `json:"plan_id" gorm:"index"`
	EnvID         int64      `json:"env_id"`
	Name          string     `json:"name"`
	CronExpr      string     `json:"cron_expr"`
	OverlapPolicy int16      `json:"overlap_policy"`
	Enabled       bool       `json:"enabled"`
	LastRunAt     *time.Time `json:"last_run_at"`
	NextRunAt     *time.Time `json:"next_run_at"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// NotificationChannel 通知渠道（type: 1=webhook 2=dingtalk 3=feishu）。
// Events: 逗号分隔（run_finished,stress_finished）。
type NotificationChannel struct {
	ID        int64     `json:"id" gorm:"primaryKey"`
	TenantID  int64     `json:"tenant_id" gorm:"index"`
	Name      string    `json:"name"`
	Type      int16     `json:"type"`
	URL       string    `json:"url"`
	Secret    string    `json:"-"`
	Events    string    `json:"events"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// IdentityProvider 外部身份源（type=oidc）；租户级（TenantID 即登录后落脚的租户）。
// 外部账号首次登录自动建用户 + 该租户 viewer 成员。
type IdentityProvider struct {
	ID           int64     `json:"id" gorm:"primaryKey"`
	TenantID     int64     `json:"tenant_id" gorm:"index"`
	Name         string    `json:"name"`
	Type         string    `json:"type" gorm:"size:16"` // oidc
	Issuer       string    `json:"issuer"`
	ClientID     string    `json:"client_id"`
	ClientSecret string    `json:"-"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// TenantQuota 租户配额上限（Limit=0 表示不限；metric 见 internal/quota）。
type TenantQuota struct {
	ID        int64     `json:"id" gorm:"primaryKey"`
	TenantID  int64     `json:"tenant_id" gorm:"uniqueIndex:idx_tq_tenant_metric"`
	Metric    string    `json:"metric" gorm:"uniqueIndex:idx_tq_tenant_metric;size:32"`
	Limit     int64     `json:"limit"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ArtifactKindFromString 将 proto ArtifactRef.kind 字符串映射为存储枚举。
func ArtifactKindFromString(s string) int16 {
	switch s {
	case "screenshot":
		return ArtifactKindScreenshot
	case "video":
		return ArtifactKindVideo
	case "trace":
		return ArtifactKindTrace
	case "har":
		return ArtifactKindHar
	case "download":
		return ArtifactKindDownload
	case "log":
		return ArtifactKindLog
	case "proto":
		return ArtifactKindProto
	case "cert":
		return ArtifactKindCert
	}
	return 0
}

// AllModels 供 AutoMigrate。
func AllModels() []any {
	return []any{
		&Tenant{}, &User{}, &TenantMember{},
		&Project{}, &Environment{}, &Variable{}, &Certificate{},
		&HttpApi{}, &TreeNode{},
		&TestCase{}, &TestPlan{}, &TestPlanItem{},
		&TestRun{}, &TestCaseResult{}, &TestStepResult{}, &Artifact{},
		&StressTestPlan{}, &StressRun{}, &StressMetricPoint{},
		&CopilotSession{}, &CopilotMessage{}, &AuditLog{},
		&TenantQuota{}, &Schedule{}, &NotificationChannel{}, &IdentityProvider{},
	}
}
