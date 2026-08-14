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
	"github.com/testpilot/testpilot/internal/tracing"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/durationpb"
)

// TriggerStress 触发一次压测：建 StressRun、按 worker_count 拆分负载到 stress Worker。
// 返回 stressRunID。压测独占调度：被选中的 Worker 在压测期间不接功能任务。
// ctx 携带调用方 span，traceparent 随 TaskAssignment 传给 Worker 续链。
func (r *Runner) TriggerStress(ctx context.Context, tenantID, planID, envID int64, triggeredBy string) (int64, error) {
	ctx, span := otel.Tracer("testpilot/scheduler").Start(ctx, "runner.trigger_stress")
	defer span.End()
	var plan model.StressTestPlan
	if err := r.db.Where("id = ? AND tenant_id = ?", planID, tenantID).First(&plan).Error; err != nil {
		return 0, apperr.NotFound(apperr.CodeNotFound, "stress plan not found")
	}
	if envID == 0 {
		envID = plan.EnvID
	}

	var env model.Environment
	envExists := r.db.Where("id = ? AND tenant_id = ?", envID, tenantID).First(&env).Error == nil

	var vars []model.Variable
	r.db.Where("tenant_id = ? AND project_id = ? AND (environment_id = 0 OR environment_id = ?)",
		tenantID, plan.ProjectID, envID).Find(&vars)

	// 目标解析：1=单接口（inline_api 下发）；2=低代码行为用例（script_ref 解析后下发源码，
	// Worker 沙箱常驻循环模式执行）
	var api model.HttpApi
	var behaviorSource, behaviorEntry string
	switch plan.TargetType {
	case 1:
		if err := r.db.Where("id = ? AND tenant_id = ?", plan.TargetID, tenantID).First(&api).Error; err != nil {
			return 0, apperr.NotFound(apperr.CodeNotFound, "target api not found")
		}
	case 2:
		var tc model.TestCase
		if err := r.db.Where("id = ? AND tenant_id = ?", plan.TargetID, tenantID).First(&tc).Error; err != nil {
			return 0, apperr.NotFound(apperr.CodeNotFound, "target case not found")
		}
		if tc.Type != int16(commonv1.TestCaseType_TEST_CASE_TYPE_LOWCODE) {
			return 0, apperr.BadRequest(apperr.CodeInvalidParam, "stress behavior target must be a lowcode case")
		}
		pcase, _, err := r.materializeCase(&tc)
		if err != nil {
			return 0, apperr.BadRequest(apperr.CodeInvalidParam, "behavior case materialize: "+err.Error())
		}
		behaviorSource = pcase.GetLowcode().GetSource()
		behaviorEntry = pcase.GetLowcode().GetEntry()
		if behaviorSource == "" {
			return 0, apperr.BadRequest(apperr.CodeInvalidParam, "behavior case has no script source")
		}
	default:
		return 0, apperr.BadRequest(apperr.CodeInvalidParam, "stress target_type must be 1(api) or 2(behavior_case)")
	}

	lp := &commonv1.LoadProfile{}
	if len(plan.LoadProfile) > 0 {
		if err := protojson.Unmarshal([]byte(plan.LoadProfile), lp); err != nil {
			return 0, apperr.BadRequest(apperr.CodeInvalidParam, "invalid load_profile: "+err.Error())
		}
	}
	// 总目标并发：ramp 末段 target；无 ramp 时 concurrency_per_worker × worker_count
	totalTarget := int(lp.GetConcurrencyPerWorker()) * max(plan.WorkerCount, 1)
	for _, st := range lp.GetRamp() {
		if int(st.GetTarget()) > totalTarget {
			totalTarget = int(st.GetTarget())
		}
	}
	if totalTarget <= 0 {
		totalTarget = 1
	}
	duration := lp.GetDuration().AsDuration()
	if duration <= 0 {
		duration = 60 * time.Second
	}

	workers := r.disp.StressWorkers(tenantID)
	n := min(max(plan.WorkerCount, 1), len(workers))
	if n == 0 {
		return 0, dispatch.ErrNoWorker
	}

	run := &model.StressRun{
		ID:           model.NextID(),
		TenantID:     tenantID,
		StressPlanID: planID,
		EnvID:        envID,
		Status:       int16(commonv1.RunStatus_RUN_STATUS_RUNNING),
		StartedAt:    time.Now(),
	}
	if err := r.db.Create(run).Error; err != nil {
		return 0, err
	}
	span.SetAttributes(attribute.Int64("run_id", run.ID), attribute.Int64("plan_id", planID),
		attribute.Int64("tenant_id", tenantID))
	logging.L.Infow("stress run triggered", "run_id", run.ID, "plan_id", planID,
		"trace_id", tracing.TraceID(ctx))
	traceparent := tracing.InjectTraceparent(ctx)

	protoPlan := &commonv1.StressTestPlan{
		Id:              idStr(plan.ID),
		TenantId:        plan.TenantID,
		ProjectId:       idStr(plan.ProjectID),
		EnvId:           idStr(envID),
		LoadProfile:     lp,
		WorkerCount:     int32(n),
		MetricsInterval: durationpb.New(time.Duration(max(plan.MetricsIntervalMs, 200)) * time.Millisecond),
	}
	switch plan.TargetType {
	case 1:
		protoPlan.Target = &commonv1.StressTestPlan_ApiId{ApiId: idStr(plan.TargetID)}
	case 2:
		protoPlan.Target = &commonv1.StressTestPlan_BehaviorCaseId{BehaviorCaseId: idStr(plan.TargetID)}
	}

	execEnv := buildExecutionEnv(&env, envExists, vars)
	timeout := duration + 120*time.Second // 宽限：发压结束后收尾

	r.disp.RegisterStressRun(run.ID, n)
	base, extra := totalTarget/n, totalTarget%n
	dispatched := 0
	for i := 0; i < n; i++ {
		assigned := base
		if i < extra {
			assigned++
		}
		if assigned <= 0 {
			assigned = 1
		}
		task := &workerv1.TaskAssignment{
			TaskId:   idStr(model.NextID()),
			RunId:    idStr(run.ID),
			TenantId: tenantID,
			TaskType: commonv1.TaskType_TASK_TYPE_STRESS,
			Timeout:  durationpb.New(timeout),
			Payload: &workerv1.TaskAssignment_Stress{
				Stress: &workerv1.StressTask{
					Plan:                protoPlan,
					WorkerIndex:         int32(i),
					AssignedConcurrency: int32(assigned),
					MetricsLabel:        idStr(run.ID),
					InlineApi:           ToProtoHTTP(&api),
					BehaviorSource:      behaviorSource,
					BehaviorEntry:       behaviorEntry,
				},
			},
			Env:         execEnv,
			Traceparent: traceparent,
		}
		if err := r.disp.DispatchStress(workers[i], task); err != nil {
			continue
		}
		dispatched++
	}
	if dispatched == 0 {
		now := time.Now()
		r.db.Model(run).Updates(map[string]any{
			"status":      int16(commonv1.RunStatus_RUN_STATUS_FAILED),
			"finished_at": &now,
			"summary":     `{"error":"no suitable worker online"}`,
		})
		return run.ID, dispatch.ErrNoWorker
	}
	return run.ID, nil
}
