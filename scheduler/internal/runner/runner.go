package runner

import (
	"context"
	"encoding/json"
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
	"google.golang.org/protobuf/types/known/structpb"
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
			dispatched += r.dispatchCase(run, tenantID, item.RefID, item.ParamOverrides, execEnv, timeout, traceparent)
		case 2: // suite 引用：展开为有序 case 序列（items 仅允许 case，无嵌套）；
			// 条目级 param_overrides 对展开出的每个 case 一致生效
			caseIDs, err := r.suiteCaseIDs(tenantID, item.RefID)
			if err != nil {
				logging.L.Warnw("suite expand failed, item skipped", "suite_id", item.RefID, "err", err)
				continue
			}
			for _, cid := range caseIDs {
				dispatched += r.dispatchCase(run, tenantID, cid, item.ParamOverrides, execEnv, timeout, traceparent)
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

// materializeCase 转换用例为 proto，并完成三类派发期解析（Worker 无 DB，只接受内联形态）：
//  1. 低代码 script_ref → 内联 source（脚本按租户从 scripts 资产库读取）；
//  2. 声明式 api_call 步骤的 api_id 引用 → inline 快照（保留 api_id 与 override）；
//  3. grpc_call 步骤的 grpc_api_id 引用 → 收集进任务级 grpc_apis 映射（按 id 字符串键控）。
//
// 解析失败即报错——dispatchCase 把该 case_result 置 FAILED，不派发残缺任务。
func (r *Runner) materializeCase(tc *model.TestCase) (*commonv1.TestCase, map[string]*commonv1.GrpcApi, error) {
	grpcAPIs := map[string]*commonv1.GrpcApi{}
	pcase := ToProtoCase(tc)
	if lc := pcase.GetLowcode(); lc != nil && lc.GetScriptRef() != "" {
		scriptID, err := strconv.ParseInt(lc.GetScriptRef(), 10, 64)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid script_ref %q: %v", lc.GetScriptRef(), err)
		}
		var script model.Script
		if err := r.db.Where("id = ? AND tenant_id = ?", scriptID, tc.TenantID).
			First(&script).Error; err != nil {
			return nil, nil, fmt.Errorf("script %d not found in tenant: %v", scriptID, err)
		}
		lc.Script = &commonv1.LowCodeCase_Source{Source: script.Content}
	}
	if dc := pcase.GetDeclarative(); dc != nil {
		if err := r.resolveAPIRefs(tc.TenantID, dc.GetSteps()); err != nil {
			return nil, nil, err
		}
		var err error
		grpcAPIs, err = r.resolveGrpcRefs(tc.TenantID, dc.GetSteps())
		if err != nil {
			return nil, nil, err
		}
	}
	return pcase, grpcAPIs, nil
}

// resolveGrpcRefs 收集 grpc_call 步骤的 grpc_api_id 引用并批量解析为任务级映射
// （key = api id 字符串，Worker 执行时按步骤引用查表）。
func (r *Runner) resolveGrpcRefs(tenantID int64, steps []*commonv1.TestStep) (map[string]*commonv1.GrpcApi, error) {
	ids := map[string]bool{}
	var collect func([]*commonv1.TestStep)
	collect = func(list []*commonv1.TestStep) {
		for _, st := range list {
			switch p := st.Params.(type) {
			case *commonv1.TestStep_GrpcCall:
				if id := p.GrpcCall.GetGrpcApiId(); id != "" {
					ids[id] = true
				}
			case *commonv1.TestStep_IfStep:
				collect(p.IfStep.GetThenSteps())
				collect(p.IfStep.GetElseSteps())
			case *commonv1.TestStep_LoopStep:
				collect(p.LoopStep.GetBodySteps())
			case *commonv1.TestStep_RetryStep:
				if b := p.RetryStep.GetBodyStep(); b != nil {
					collect([]*commonv1.TestStep{b})
				}
			}
		}
	}
	collect(steps)
	if len(ids) == 0 {
		return map[string]*commonv1.GrpcApi{}, nil
	}
	keys := make([]int64, 0, len(ids))
	for raw := range ids {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid grpc_api_id %q in steps: %v", raw, err)
		}
		keys = append(keys, id)
	}
	var rows []model.GrpcApi
	if err := r.db.Where("tenant_id = ? AND id IN ?", tenantID, keys).Find(&rows).Error; err != nil {
		return nil, err
	}
	byID := make(map[int64]model.GrpcApi, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	out := make(map[string]*commonv1.GrpcApi, len(keys))
	for _, key := range keys {
		row, ok := byID[key]
		if !ok {
			return nil, fmt.Errorf("grpc api %d not found in tenant", key)
		}
		out[strconv.FormatInt(key, 10)] = ToProtoGrpc(&row)
	}
	return out, nil
}

// resolveAPIRefs 把步骤树（含 if/loop/retry 嵌套）中 api_id 引用批量解析为 inline 快照。
// 已有 inline 的步骤保持原样（inline 是显式快照，不覆盖用户手写内容）。
func (r *Runner) resolveAPIRefs(tenantID int64, steps []*commonv1.TestStep) error {
	ids := map[string]bool{}
	var collect func([]*commonv1.TestStep)
	collect = func(list []*commonv1.TestStep) {
		for _, st := range list {
			switch p := st.Params.(type) {
			case *commonv1.TestStep_ApiCall:
				if id := p.ApiCall.GetApiId(); id != "" && p.ApiCall.Inline == nil {
					ids[id] = true
				}
			case *commonv1.TestStep_IfStep:
				collect(p.IfStep.GetThenSteps())
				collect(p.IfStep.GetElseSteps())
			case *commonv1.TestStep_LoopStep:
				collect(p.LoopStep.GetBodySteps())
			case *commonv1.TestStep_RetryStep:
				if b := p.RetryStep.GetBodyStep(); b != nil {
					collect([]*commonv1.TestStep{b})
				}
			}
		}
	}
	collect(steps)
	if len(ids) == 0 {
		return nil
	}

	keys := make([]int64, 0, len(ids))
	for raw := range ids {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid api_id %q in steps: %v", raw, err)
		}
		keys = append(keys, id)
	}
	var apis []model.HttpApi
	if err := r.db.Where("tenant_id = ? AND id IN ?", tenantID, keys).Find(&apis).Error; err != nil {
		return err
	}
	byID := make(map[int64]*model.HttpApi, len(apis))
	for i := range apis {
		byID[apis[i].ID] = &apis[i]
	}

	var fill func([]*commonv1.TestStep) error
	fill = func(list []*commonv1.TestStep) error {
		for _, st := range list {
			switch p := st.Params.(type) {
			case *commonv1.TestStep_ApiCall:
				id := p.ApiCall.GetApiId()
				if id == "" || p.ApiCall.Inline != nil {
					continue
				}
				n, _ := strconv.ParseInt(id, 10, 64) // 已在 collect 阶段校验
				api, ok := byID[n]
				if !ok {
					return fmt.Errorf("api %d not found in tenant", n)
				}
				p.ApiCall.Inline = ToProtoHTTP(api)
			case *commonv1.TestStep_IfStep:
				if err := fill(p.IfStep.GetThenSteps()); err != nil {
					return err
				}
				if err := fill(p.IfStep.GetElseSteps()); err != nil {
					return err
				}
			case *commonv1.TestStep_LoopStep:
				if err := fill(p.LoopStep.GetBodySteps()); err != nil {
					return err
				}
			case *commonv1.TestStep_RetryStep:
				if b := p.RetryStep.GetBodyStep(); b != nil {
					if err := fill([]*commonv1.TestStep{b}); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	return fill(steps)
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

// dispatchCase 加载用例、预建 case result、应用条目级参数覆盖并派发；返回成功派发数（0 或 1）。
func (r *Runner) dispatchCase(run *model.TestRun, tenantID, caseID int64, overrides model.JSON,
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

	// 派发期解析：script_ref → source；api_id → inline；grpc_api_id → 任务级映射。
	pcase, grpcAPIs, failErr := r.materializeCase(&tc)
	if failErr != nil {
		r.db.Model(cr).Updates(map[string]any{
			"status": int16(commonv1.CaseStatus_CASE_STATUS_FAILED),
			"error":  failErr.Error(),
		})
		return 0
	}
	taskEnv, failErr := applyOverrides(overrides, pcase, execEnv)
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
				GrpcApis:     grpcAPIs,
			},
		},
		Env:         taskEnv,
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

// applyOverrides 应用计划条目级 param_overrides（JSON 对象）：
//   - 低代码：深合并进 case `parameters`（SDK 经 `ctx.parameters` 读取；覆盖值优先于用例默认）；
//   - 全部用例类型：追加为任务级模板变量（`{{key}}` 可渲染；非字符串值 JSON 序列化），
//     排在共享 execEnv 变量之后——Worker 按序取末值，覆盖值优先级最高。
//
// 不修改共享 execEnv（每个任务独立副本）；未传覆盖时原样返回。
func applyOverrides(raw model.JSON, pcase *commonv1.TestCase,
	execEnv *workerv1.ExecutionEnv) (*workerv1.ExecutionEnv, error) {
	if len(raw) == 0 {
		return execEnv, nil
	}
	var ov map[string]any
	if err := json.Unmarshal([]byte(raw), &ov); err != nil {
		return execEnv, fmt.Errorf("invalid param_overrides: %v", err)
	}
	if len(ov) == 0 {
		return execEnv, nil
	}

	if lc := pcase.GetLowcode(); lc != nil {
		merged := mergeMaps(lc.GetParameters().AsMap(), ov)
		s, err := structpb.NewStruct(merged)
		if err != nil {
			return execEnv, fmt.Errorf("param_overrides merge: %v", err)
		}
		lc.Parameters = s
	}

	vars := make([]*commonv1.Variable, 0, len(execEnv.GetVariables())+len(ov))
	vars = append(vars, execEnv.GetVariables()...)
	for k, v := range ov {
		vars = append(vars, &commonv1.Variable{Key: k, Value: overrideString(v)})
	}
	return &workerv1.ExecutionEnv{
		Environment: execEnv.GetEnvironment(),
		BaseUrl:     execEnv.GetBaseUrl(),
		Variables:   vars,
	}, nil
}

// mergeMaps 深合并：ov 覆盖 base；嵌套对象递归合并。
func mergeMaps(base, ov map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(ov))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range ov {
		if bm, ok := out[k].(map[string]any); ok {
			if vm, ok := v.(map[string]any); ok {
				out[k] = mergeMaps(bm, vm)
				continue
			}
		}
		out[k] = v
	}
	return out
}

// overrideString 把覆盖值转字符串（模板变量形态）；复杂值 JSON 序列化。
func overrideString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		if b, err := json.Marshal(v); err == nil {
			return string(b)
		}
		return fmt.Sprint(v)
	}
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
