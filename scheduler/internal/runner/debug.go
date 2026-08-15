package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	workerv1 "github.com/testpilot/testpilot/gen/worker/v1"
	"github.com/testpilot/testpilot/internal/apperr"
	"github.com/testpilot/testpilot/internal/model"
	"github.com/testpilot/testpilot/internal/quota"
	"gorm.io/gorm"
	"github.com/testpilot/testpilot/internal/tracing"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"
)

// structToAny structpb.Struct → JSON 友好值（调试响应直出）。
func structToAny(s *structpb.Struct) any {
	if s == nil {
		return nil
	}
	raw, err := protojson.Marshal(s)
	if err != nil {
		return nil
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	return v
}

// assertionsToAny 断言结果 → JSON 友好值。
func assertionsToAny(list []*commonv1.AssertionResult) any {
	if len(list) == 0 {
		return nil
	}
	out := make([]any, 0, len(list))
	for _, a := range list {
		raw, err := protojson.Marshal(a)
		if err != nil {
			continue
		}
		var v any
		if json.Unmarshal(raw, &v) == nil {
			out = append(out, v)
		}
	}
	return out
}

// DebugRequest 接口调试请求（REST 层 wire 形态的 Go 结构）。
type DebugRequest struct {
	ProjectID int64
	APIID     int64
	Method    int16
	URI       string
	Params    []*commonv1.KeyValue
	Headers   []*commonv1.KeyValue
	Body      *commonv1.BodySpec
	EnvID     int64
	TimeoutMs int
}

// DebugResult 调试响应（步失败也返回 200 外层，状态在 body）。
type DebugResult struct {
	RunID        string           `json:"run_id"`
	CaseResultID string           `json:"case_result_id"`
	Status       int16            `json:"status"`
	DurationMs   int64            `json:"duration_ms"`
	Error        string           `json:"error"`
	Step         *DebugStepResult `json:"step"`
}

// DebugStepResult 单步快照（对齐 step result 落库字段）。
type DebugStepResult struct {
	StepPath   string   `json:"step_path"`
	Status     int16    `json:"status"`
	DurationMs int64    `json:"duration_ms"`
	Request    any      `json:"request,omitempty"`
	Response   any      `json:"response,omitempty"`
	Assertions any      `json:"assertions,omitempty"`
	Logs       []string `json:"logs,omitempty"`
}

// Debug 执行一次接口调试：构造单步声明式 case 派发并同步等待结果。
// api_id 与 uri 二选一（api_id 时其余字段为覆盖）；env 回退：env_id → 项目首个 env。
func (r *Runner) Debug(ctx context.Context, tenantID int64, in DebugRequest) (*DebugResult, error) {
	// 基础接口：api_id 加载（租户过滤）或裸 uri
	api := &commonv1.HttpApi{}
	if in.APIID != 0 {
		var row model.HttpApi
		if err := r.db.Where("id = ? AND tenant_id = ?", in.APIID, tenantID).First(&row).Error; err != nil {
			return nil, apperr.NotFound(apperr.CodeNotFound, "api not found")
		}
		api = ToProtoHTTP(&row)
	} else if in.URI == "" {
		return nil, apperr.BadRequest(apperr.CodeInvalidParam, "uri required when api_id absent")
	}

	// 覆盖合并（method/uri/params/headers/body）
	if in.Method != 0 {
		api.Method = commonv1.HttpMethod(in.Method)
	}
	if in.URI != "" {
		api.Uri = in.URI
	}
	if in.Params != nil {
		api.Params = in.Params
	}
	if in.Headers != nil {
		api.Headers = in.Headers
	}
	if in.Body != nil {
		api.Body = in.Body
	}

	// 环境回退 + 变量
	envID := in.EnvID
	var env model.Environment
	if envID != 0 {
		r.db.Where("id = ? AND tenant_id = ?", envID, tenantID).First(&env)
	} else {
		r.db.Where("tenant_id = ? AND project_id = ?", tenantID, in.ProjectID).
			Order("id asc").Limit(1).First(&env)
	}
	envExists := env.ID != 0
	var vars []model.Variable
	r.db.Where("tenant_id = ? AND project_id = ? AND (environment_id = 0 OR environment_id = ?)",
		tenantID, in.ProjectID, env.ID).Find(&vars)
	execEnv := buildExecutionEnv(&env, envExists, vars)

	// 配额检查与 run/cr 创建同事务（CheckTx 串行化，防并发穿透；
	// 创建失败时配额计数一并回滚）
	run := &model.TestRun{
		ID:          model.NextID(),
		TenantID:    tenantID,
		EnvID:       env.ID,
		Status:      int16(commonv1.RunStatus_RUN_STATUS_RUNNING),
		Trigger:     int16(commonv1.TriggerType_TRIGGER_TYPE_MANUAL),
		TriggeredBy: "debug",
		StartedAt:   time.Now(),
	}
	cr := &model.TestCaseResult{
		ID:       model.NextID(),
		TenantID: tenantID,
		RunID:    run.ID,
		CaseID:   in.APIID, // 0=未保存的临时请求
		Status:   int16(commonv1.CaseStatus_CASE_STATUS_RUNNING),
	}
	if err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := quota.CheckTx(tx, tenantID, quota.MetricConcurrentRuns, 1); err != nil {
			return err
		}
		if err := quota.CheckTx(tx, tenantID, quota.MetricMonthlyRuns, 1); err != nil {
			return err
		}
		if err := tx.Create(run).Error; err != nil {
			return err
		}
		return tx.Create(cr).Error
	}); err != nil {
		return nil, err
	}

	task := &workerv1.TaskAssignment{
		TaskId:   idStr(model.NextID()),
		RunId:    idStr(run.ID),
		TenantId: tenantID,
		TaskType: commonv1.TaskType_TASK_TYPE_FUNCTIONAL_DECLARATIVE,
		Timeout:  durationpb.New(30 * time.Second),
		Payload: &workerv1.TaskAssignment_Functional{
			Functional: &workerv1.FunctionalTask{
				Case: &commonv1.TestCase{
					Id:   "debug",
					Type: commonv1.TestCaseType_TEST_CASE_TYPE_DECLARATIVE,
					Name: "api-debug",
					Definition: &commonv1.TestCase_Declarative{Declarative: &commonv1.DeclarativeCase{
						Steps: []*commonv1.TestStep{{
							Id:   "1",
							Name: "debug",
							Params: &commonv1.TestStep_ApiCall{ApiCall: &commonv1.ApiCallStep{
								Inline: api,
							}},
						}},
					}},
				},
				CaseResultId: idStr(cr.ID),
			},
		},
		Env:         execEnv,
		Traceparent: tracing.InjectTraceparent(ctx),
	}

	waiter := r.disp.RegisterWaiter(task.TaskId) // 先注册后派发（防结果竞态）
	if err := r.disp.Dispatch(task); err != nil {
		r.disp.TakeWaiter(task.TaskId) // 消费掉等待器，防 waiters 无界泄漏
		now := time.Now()
		r.db.Model(cr).Updates(map[string]any{
			"status": int16(commonv1.CaseStatus_CASE_STATUS_FAILED), "error": err.Error()})
		r.db.Model(run).Updates(map[string]any{
			"status": int16(commonv1.RunStatus_RUN_STATUS_FAILED), "finished_at": &now})
		return nil, err
	}

	timeout := 30 * time.Second
	if in.TimeoutMs >= 1000 && in.TimeoutMs <= 30000 {
		timeout = time.Duration(in.TimeoutMs) * time.Millisecond
	}
	var res *workerv1.TaskResult
	select {
	case res = <-waiter:
	case <-time.After(timeout):
		r.disp.Cancel(task.TaskId, "debug timeout")
		now := time.Now()
		r.disp.TakeWaiter(task.TaskId)
		// case result 一并收尾，避免 UI 上永久 RUNNING
		r.db.Model(cr).Updates(map[string]any{
			"status": int16(commonv1.CaseStatus_CASE_STATUS_FAILED), "error": "debug timed out"})
		r.db.Model(run).Updates(map[string]any{
			"status": int16(commonv1.RunStatus_RUN_STATUS_ABORTED), "finished_at": &now})
		return nil, apperr.New(504, apperr.CodeDebugTimeout, fmt.Sprintf("debug timed out after %s", timeout))
	}

	out := &DebugResult{
		RunID:        idStr(run.ID),
		CaseResultID: idStr(cr.ID),
		Status:       int16(res.GetStatus()),
		DurationMs:   res.GetDuration().AsDuration().Milliseconds(),
		Error:        res.GetError(),
	}
	if len(res.GetCaseResults()) > 0 {
		c := res.GetCaseResults()[0]
		out.Status = int16(c.GetStatus())
		out.Error = c.GetError()
	}
	if len(res.GetStepResults()) > 0 {
		s := res.GetStepResults()[0]
		out.Step = &DebugStepResult{
			StepPath:   s.GetStepPath(),
			Status:     int16(s.GetStatus()),
			DurationMs: s.GetDuration().AsDuration().Milliseconds(),
			Request:    structToAny(s.GetRequest()),
			Response:   structToAny(s.GetResponse()),
			Assertions: assertionsToAny(s.GetAssertions()),
			Logs:       s.GetLogs(),
		}
	}
	return out, nil
}
