package runner

import (
	"context"
	"fmt"
	"strconv"
	"time"

	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	workerv1 "github.com/testpilot/testpilot/gen/worker/v1"
	"github.com/testpilot/testpilot/internal/apperr"
	"github.com/testpilot/testpilot/internal/dispatch"
	"github.com/testpilot/testpilot/internal/logging"
	"github.com/testpilot/testpilot/internal/model"
	"github.com/testpilot/testpilot/internal/quota"
	"github.com/testpilot/testpilot/internal/tracing"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/protobuf/types/known/durationpb"
	"gorm.io/gorm"
)

// Runner 负责触发运行：建 run、逐 case 派发任务给 Worker。
type Runner struct {
	db   *gorm.DB
	disp *dispatch.Dispatcher
}

func New(db *gorm.DB, disp *dispatch.Dispatcher) *Runner {
	return &Runner{db: db, disp: disp}
}

func taskTypeFor(caseType int16) commonv1.TaskType {
	if caseType == int16(commonv1.TestCaseType_TEST_CASE_TYPE_LOWCODE) {
		return commonv1.TaskType_TASK_TYPE_FUNCTIONAL_LOWCODE
	}
	return commonv1.TaskType_TASK_TYPE_FUNCTIONAL_DECLARATIVE
}

// Trigger 触发一次 TestPlan 运行，返回 runID。ctx 携带调用方 span（REST/gRPC/cron），
// traceparent 随 TaskAssignment 传给 Worker 续链。
func (r *Runner) Trigger(ctx context.Context, tenantID, planID, envID int64, trigger int16, triggeredBy string) (int64, error) {
	ctx, span := otel.Tracer("testpilot/scheduler").Start(ctx, "runner.trigger")
	defer span.End()
	if err := quota.Check(r.db, tenantID, quota.MetricConcurrentRuns, 1); err != nil {
		return 0, err
	}
	if err := quota.Check(r.db, tenantID, quota.MetricMonthlyRuns, 1); err != nil {
		return 0, err
	}
	var plan model.TestPlan
	if err := r.db.Where("id = ? AND tenant_id = ?", planID, tenantID).First(&plan).Error; err != nil {
		return 0, apperr.NotFound(apperr.CodePlanNotFound, "plan not found")
	}
	if envID == 0 {
		envID = plan.EnvID
	}

	var env model.Environment
	envExists := r.db.Where("id = ? AND tenant_id = ?", envID, tenantID).First(&env).Error == nil

	var vars []model.Variable
	r.db.Where("tenant_id = ? AND project_id = ? AND (environment_id = 0 OR environment_id = ?)",
		tenantID, plan.ProjectID, envID).Find(&vars)

	var items []model.TestPlanItem
	r.db.Where("plan_id = ? AND enabled = ?", planID, true).Order("\"order\" asc").Find(&items)
	if len(items) == 0 {
		return 0, apperr.BadRequest(apperr.CodePlanNoItems, "plan has no enabled items")
	}

	run := &model.TestRun{
		ID:          model.NextID(),
		TenantID:    tenantID,
		PlanID:      planID,
		EnvID:       envID,
		Status:      int16(commonv1.RunStatus_RUN_STATUS_RUNNING),
		Trigger:     trigger,
		TriggeredBy: triggeredBy,
		StartedAt:   time.Now(),
	}
	if err := r.db.Create(run).Error; err != nil {
		return 0, err
	}
	span.SetAttributes(attribute.Int64("run_id", run.ID), attribute.Int64("plan_id", planID),
		attribute.Int64("tenant_id", tenantID))
	logging.L.Infow("run triggered", "run_id", run.ID, "plan_id", planID,
		"trigger", trigger, "trace_id", tracing.TraceID(ctx))
	traceparent := tracing.InjectTraceparent(ctx)

	execEnv := buildExecutionEnv(&env, envExists, vars)
	timeout := time.Duration(plan.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}

	dispatched := 0
	for _, item := range items {
		switch item.RefType {
		case 1: // case 引用
			dispatched += r.dispatchCase(run, tenantID, item.RefID, execEnv, timeout, traceparent)
		case 2: // suite 引用：展开为有序 case 序列（items 仅允许 case，无嵌套）
			caseIDs, err := r.suiteCaseIDs(tenantID, item.RefID)
			if err != nil {
				logging.L.Warnw("suite expand failed, item skipped", "suite_id", item.RefID, "err", err)
				continue
			}
			for _, cid := range caseIDs {
				dispatched += r.dispatchCase(run, tenantID, cid, execEnv, timeout, traceparent)
			}
		default:
			logging.L.Warnw("plan item with unknown ref_type skipped", "ref_type", item.RefType)
		}
	}

	if dispatched == 0 {
		now := time.Now()
		r.db.Model(run).Updates(map[string]any{
			"status":      int16(commonv1.RunStatus_RUN_STATUS_FAILED),
			"finished_at": &now,
			"summary":     `{"total":0,"passed":0,"failed":0,"skipped":0,"error":"no suitable worker online"}`,
		})
		return run.ID, dispatch.ErrNoWorker
	}
	return run.ID, nil
}

// materializeCase 转换用例为 proto，并把低代码 script_ref 解析为内联 source
// （Worker 引擎只接受 source；脚本内容按租户从 scripts 资产库读取，不进 TaskAssignment 之外的任何落盘）。
func (r *Runner) materializeCase(tc *model.TestCase) (*commonv1.TestCase, error) {
	pcase := ToProtoCase(tc)
	lc := pcase.GetLowcode()
	if lc == nil || lc.GetScriptRef() == "" {
		return pcase, nil
	}
	scriptID, err := strconv.ParseInt(lc.GetScriptRef(), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid script_ref %q: %v", lc.GetScriptRef(), err)
	}
	var script model.Script
	if err := r.db.Where("id = ? AND tenant_id = ?", scriptID, tc.TenantID).
		First(&script).Error; err != nil {
		return nil, fmt.Errorf("script %d not found in tenant: %v", scriptID, err)
	}
	lc.Script = &commonv1.LowCodeCase_Source{Source: script.Content}
	return pcase, nil
}

// suiteCaseIDs 展开套件为有序 case id 列表；套件不存在或不属于该租户返回错误。
func (r *Runner) suiteCaseIDs(tenantID, suiteID int64) ([]int64, error) {
	var s model.TestSuite
	if err := r.db.Where("id = ? AND tenant_id = ?", suiteID, tenantID).First(&s).Error; err != nil {
		return nil, err
	}
	var items []model.TestSuiteItem
	if err := r.db.Where("suite_id = ?", s.ID).Order("\"order\" asc").Find(&items).Error; err != nil {
		return nil, err
	}
	out := make([]int64, 0, len(items))
	for _, it := range items {
		out = append(out, it.CaseID)
	}
	return out, nil
}

// dispatchCase 加载用例、预建 case result 并派发；返回成功派发数（0 或 1）。
func (r *Runner) dispatchCase(run *model.TestRun, tenantID, caseID int64,
	execEnv *workerv1.ExecutionEnv, timeout time.Duration, traceparent string) int {
	var tc model.TestCase
	if err := r.db.Where("id = ? AND tenant_id = ?", caseID, tenantID).First(&tc).Error; err != nil {
		logging.L.Warnw("case not found, item skipped", "case_id", caseID, "err", err)
		return 0
	}
	cr := &model.TestCaseResult{
		ID:       model.NextID(),
		TenantID: tenantID,
		RunID:    run.ID,
		CaseID:   tc.ID,
		Status:   int16(commonv1.CaseStatus_CASE_STATUS_RUNNING),
	}
	r.db.Create(cr)

	// 低代码 script_ref 在此解析为内联 source（Worker 无 DB，只认 source）。
	pcase, failErr := r.materializeCase(&tc)
	if failErr != nil {
		r.db.Model(cr).Updates(map[string]any{
			"status": int16(commonv1.CaseStatus_CASE_STATUS_FAILED),
			"error":  failErr.Error(),
		})
		return 0
	}

	task := &workerv1.TaskAssignment{
		TaskId:   idStr(model.NextID()),
		RunId:    idStr(run.ID),
		TenantId: tenantID,
		TaskType: taskTypeFor(tc.Type),
		Timeout:  durationpb.New(timeout),
		Payload: &workerv1.TaskAssignment_Functional{
			Functional: &workerv1.FunctionalTask{
				Case:         pcase,
				CaseResultId: idStr(cr.ID),
			},
		},
		Env:         execEnv,
		Traceparent: traceparent,
	}
	if err := r.disp.Dispatch(task); err != nil {
		r.db.Model(cr).Updates(map[string]any{
			"status": int16(commonv1.CaseStatus_CASE_STATUS_FAILED),
			"error":  err.Error(),
		})
		return 0
	}
	return 1
}

func buildExecutionEnv(env *model.Environment, exists bool, vars []model.Variable) *workerv1.ExecutionEnv {
	ee := &workerv1.ExecutionEnv{}
	if exists {
		ee.Environment = ToProtoEnvironment(env)
		ee.BaseUrl = env.BaseURL
	}
	pv := make([]*commonv1.Variable, 0, len(vars))
	for i := range vars {
		pv = append(pv, ToProtoVariable(&vars[i]))
	}
	ee.Variables = pv
	return ee
}
