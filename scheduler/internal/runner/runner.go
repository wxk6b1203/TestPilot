package runner

import (
	"context"
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
		if item.RefType != 1 { // MVP：仅 case 引用（suite 展开后续）
			continue
		}
		var tc model.TestCase
		if err := r.db.Where("id = ? AND tenant_id = ?", item.RefID, tenantID).First(&tc).Error; err != nil {
			continue
		}
		cr := &model.TestCaseResult{
			ID:       model.NextID(),
			TenantID: tenantID,
			RunID:    run.ID,
			CaseID:   tc.ID,
			Status:   int16(commonv1.CaseStatus_CASE_STATUS_RUNNING),
		}
		r.db.Create(cr)

		task := &workerv1.TaskAssignment{
			TaskId:   idStr(model.NextID()),
			RunId:    idStr(run.ID),
			TenantId: tenantID,
			TaskType: taskTypeFor(tc.Type),
			Timeout:  durationpb.New(timeout),
			Payload: &workerv1.TaskAssignment_Functional{
				Functional: &workerv1.FunctionalTask{
					Case:         ToProtoCase(&tc),
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
			continue
		}
		dispatched++
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
