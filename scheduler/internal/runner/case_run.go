package runner

import (
	"context"
	"strconv"
	"time"

	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	"github.com/testpilot/testpilot/internal/apperr"
	"github.com/testpilot/testpilot/internal/dispatch"
	"github.com/testpilot/testpilot/internal/logging"
	"github.com/testpilot/testpilot/internal/model"
	"github.com/testpilot/testpilot/internal/quota"
	"github.com/testpilot/testpilot/internal/tracing"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"gorm.io/gorm"
)

// RunCase 直接触发单个用例运行（用例编辑器「运行」按钮）。
// envID=0 时自动取该项目的第一个环境；无环境仍可运行（base_url 为空）。
func (r *Runner) RunCase(ctx context.Context, tenantID, caseID, envID int64,
	triggeredBy string) (int64, error) {
	ctx, span := otel.Tracer("testpilot/scheduler").Start(ctx, "runner.run_case")
	defer span.End()

	if err := quota.Check(r.db, tenantID, quota.MetricConcurrentRuns, 1); err != nil {
		return 0, err
	}
	if err := quota.Check(r.db, tenantID, quota.MetricMonthlyRuns, 1); err != nil {
		return 0, err
	}

	var tc model.TestCase
	if err := r.db.Where("id = ? AND tenant_id = ?", caseID, tenantID).First(&tc).Error; err != nil {
		return 0, apperr.NotFound(apperr.CodeCaseNotFound, "case not found")
	}

	var env model.Environment
	if envID == 0 {
		r.db.Where("tenant_id = ? AND project_id = ?", tenantID, tc.ProjectID).
			Order("id asc").Limit(1).First(&env)
	} else {
		r.db.Where("id = ? AND tenant_id = ?", envID, tenantID).First(&env)
	}
	envExists := env.ID != 0

	var vars []model.Variable
	r.db.Where("tenant_id = ? AND project_id = ? AND (environment_id = 0 OR environment_id = ?)",
		tenantID, tc.ProjectID, env.ID).Find(&vars)

	run := &model.TestRun{
		ID:          model.NextID(),
		TenantID:    tenantID,
		PlanID:      0, // 单用例直接运行，无 plan
		EnvID:       env.ID,
		Status:      int16(commonv1.RunStatus_RUN_STATUS_RUNNING),
		Trigger:     int16(commonv1.TriggerType_TRIGGER_TYPE_MANUAL),
		TriggeredBy: triggeredBy,
		StartedAt:   time.Now(),
	}
	if err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := quota.CheckTx(tx, tenantID, quota.MetricConcurrentRuns, 1); err != nil {
			return err
		}
		if err := quota.CheckTx(tx, tenantID, quota.MetricMonthlyRuns, 1); err != nil {
			return err
		}
		return tx.Create(run).Error
	}); err != nil {
		return 0, err
	}
	r.publishProject(tc.ProjectID, "run_created", map[string]any{
		"run_id": strconv.FormatInt(run.ID, 10),
		"status": run.Status,
	})
	span.SetAttributes(attribute.Int64("run_id", run.ID), attribute.Int64("case_id", caseID),
		attribute.Int64("tenant_id", tenantID))
	logging.L.Infow("case run triggered", "run_id", run.ID, "case_id", caseID,
		"trigger", run.Trigger, "trace_id", tracing.TraceID(ctx))
	traceparent := tracing.InjectTraceparent(ctx)

	execEnv := buildExecutionEnv(&env, envExists, vars)
	timeout := 5 * time.Minute
	dispatched := r.dispatchCase(run, tenantID, caseID, nil, execEnv, timeout, traceparent)
	if dispatched == 0 {
		now := time.Now()
		r.db.Model(run).Updates(map[string]any{
			"status":      int16(commonv1.RunStatus_RUN_STATUS_FAILED),
			"finished_at": &now,
			"summary":     `{"total":0,"passed":0,"failed":0,"skipped":0,"error":"no suitable worker online"}`,
		})
		r.publishProject(tc.ProjectID, "run_updated", map[string]any{
			"run_id": strconv.FormatInt(run.ID, 10),
			"status": int16(commonv1.RunStatus_RUN_STATUS_FAILED),
		})
		return run.ID, dispatch.ErrNoWorker
	}
	return run.ID, nil
}
