package runner

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	workerv1 "github.com/testpilot/testpilot/gen/worker/v1"
	"github.com/testpilot/testpilot/internal/apperr"
	"github.com/testpilot/testpilot/internal/db"
	"github.com/testpilot/testpilot/internal/dispatch"
	"github.com/testpilot/testpilot/internal/model"
	"github.com/testpilot/testpilot/internal/quota"
	"gorm.io/gorm"
)

func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"), "", db.Pool{})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func mkWorker(id string, tenant int64, caps ...commonv1.Capability) *dispatch.Worker {
	cs := make([]int32, 0, len(caps))
	for _, c := range caps {
		cs = append(cs, int32(c))
	}
	return &dispatch.Worker{
		ID:             id,
		TenantID:       tenant,
		Capabilities:   cs,
		MaxConcurrency: 2,
		Send:           make(chan *workerv1.SchedulerCommand, 8),
	}
}

// planFixture 一套 tenant 1 下的 project/env/plan/case/item（plan 带 1 个 enabled case item）。
type planFixture struct {
	projID, envID, planID, caseID int64
}

func seedPlanData(t *testing.T, d *gorm.DB) planFixture {
	t.Helper()
	proj := model.Project{ID: model.NextID(), TenantID: 1, Name: "proj"}
	if err := d.Create(&proj).Error; err != nil {
		t.Fatal(err)
	}
	env := model.Environment{ID: model.NextID(), TenantID: 1, ProjectID: proj.ID,
		Name: "staging", BaseURL: "https://api.example.com"}
	if err := d.Create(&env).Error; err != nil {
		t.Fatal(err)
	}
	tc := model.TestCase{
		ID: model.NextID(), TenantID: 1, ProjectID: proj.ID,
		Type: int16(commonv1.TestCaseType_TEST_CASE_TYPE_DECLARATIVE),
		Name: "login", Definition: model.JSON(`{}`),
	}
	if err := d.Create(&tc).Error; err != nil {
		t.Fatal(err)
	}
	plan := model.TestPlan{
		ID: model.NextID(), TenantID: 1, ProjectID: proj.ID, EnvID: env.ID,
		Name: "plan", TimeoutMs: 30000,
	}
	if err := d.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	item := model.TestPlanItem{
		ID: model.NextID(), TenantID: 1, PlanID: plan.ID,
		RefType: 1, RefID: tc.ID, Enabled: true, Order: 1,
	}
	if err := d.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	return planFixture{proj.ID, env.ID, plan.ID, tc.ID}
}

func TestTaskTypeFor(t *testing.T) {
	if got := taskTypeFor(int16(commonv1.TestCaseType_TEST_CASE_TYPE_LOWCODE)); got != commonv1.TaskType_TASK_TYPE_FUNCTIONAL_LOWCODE {
		t.Fatalf("lowcode → %v", got)
	}
	if got := taskTypeFor(int16(commonv1.TestCaseType_TEST_CASE_TYPE_DECLARATIVE)); got != commonv1.TaskType_TASK_TYPE_FUNCTIONAL_DECLARATIVE {
		t.Fatalf("declarative → %v", got)
	}
	if got := taskTypeFor(0); got != commonv1.TaskType_TASK_TYPE_FUNCTIONAL_DECLARATIVE {
		t.Fatalf("unspecified → %v", got)
	}
}

func TestTriggerSuccess(t *testing.T) {
	d := openTestDB(t)
	fx := seedPlanData(t, d)

	// 变量：项目级(environment_id=0) + 目标 env 应进入 ExecutionEnv；其他 env / 其他项目不应进入。
	vars := []model.Variable{
		{ID: model.NextID(), TenantID: 1, ProjectID: fx.projID, EnvironmentID: 0, Key: "BASE_HOST", Value: "example.com"},
		{ID: model.NextID(), TenantID: 1, ProjectID: fx.projID, EnvironmentID: fx.envID, Key: "TOKEN", Value: "t123"},
		{ID: model.NextID(), TenantID: 1, ProjectID: fx.projID, EnvironmentID: 0, Key: "SECRET", Value: "should-hide", Sensitive: true, SecretRef: "vault://k"},
		{ID: model.NextID(), TenantID: 1, ProjectID: fx.projID, EnvironmentID: model.NextID(), Key: "OTHER_ENV", Value: "x"},
		{ID: model.NextID(), TenantID: 1, ProjectID: model.NextID(), EnvironmentID: 0, Key: "OTHER_PROJ", Value: "y"},
	}
	for i := range vars {
		if err := d.Create(&vars[i]).Error; err != nil {
			t.Fatal(err)
		}
	}

	disp := dispatch.New(d)
	w := mkWorker("w1", 0, commonv1.Capability_CAPABILITY_FUNCTIONAL)
	if err := disp.Register(w); err != nil {
		t.Fatal(err)
	}
	r := New(d, disp)

	// envID=0 → 回退 plan.EnvID
	runID, err := r.Trigger(context.Background(), 1, fx.planID, 0,
		int16(commonv1.TriggerType_TRIGGER_TYPE_MANUAL), "tester")
	if err != nil {
		t.Fatal(err)
	}
	if runID == 0 {
		t.Fatal("runID should be non-zero")
	}

	var run model.TestRun
	if err := d.First(&run, runID).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != int16(commonv1.RunStatus_RUN_STATUS_RUNNING) {
		t.Fatalf("run status=%d, want RUNNING", run.Status)
	}
	if run.Trigger != int16(commonv1.TriggerType_TRIGGER_TYPE_MANUAL) || run.TriggeredBy != "tester" {
		t.Fatalf("trigger not propagated: trigger=%d by=%q", run.Trigger, run.TriggeredBy)
	}
	if run.EnvID != fx.envID {
		t.Fatalf("envID=0 should fall back to plan env %d, got %d", fx.envID, run.EnvID)
	}
	if run.StartedAt.IsZero() {
		t.Fatal("started_at should be set")
	}

	var cr model.TestCaseResult
	if err := d.Where("run_id = ?", runID).First(&cr).Error; err != nil {
		t.Fatal(err)
	}
	if cr.Status != int16(commonv1.CaseStatus_CASE_STATUS_RUNNING) || cr.CaseID != fx.caseID {
		t.Fatalf("case result wrong: %+v", cr)
	}

	// Dispatch 同步发送，Trigger 返回后通道必有且仅有一条。
	var cmd *workerv1.SchedulerCommand
	select {
	case cmd = <-w.Send:
	default:
		t.Fatal("worker should have received a task")
	}
	select {
	case <-w.Send:
		t.Fatal("exactly one task expected")
	default:
	}

	task := cmd.GetTask()
	if task == nil {
		t.Fatal("command should carry a task assignment")
	}
	if task.GetRunId() != fmt.Sprint(runID) || task.GetTenantId() != 1 {
		t.Fatalf("task run/tenant mismatch: run=%v tenant=%v", task.GetRunId(), task.GetTenantId())
	}
	if task.GetTaskType() != commonv1.TaskType_TASK_TYPE_FUNCTIONAL_DECLARATIVE {
		t.Fatalf("task type=%v", task.GetTaskType())
	}
	if got := task.GetTimeout().AsDuration(); got != 30*time.Second {
		t.Fatalf("timeout=%v, want 30s", got)
	}
	ft := task.GetFunctional()
	if ft == nil || ft.GetCase().GetId() != fmt.Sprint(fx.caseID) {
		t.Fatalf("functional payload wrong: %+v", ft)
	}
	if ft.GetCaseResultId() != fmt.Sprint(cr.ID) {
		t.Fatalf("case_result_id=%v want %v", ft.GetCaseResultId(), cr.ID)
	}

	ee := task.GetEnv()
	if ee.GetBaseUrl() != "https://api.example.com" {
		t.Fatalf("base_url=%q", ee.GetBaseUrl())
	}
	if ee.GetEnvironment().GetId() != fmt.Sprint(fx.envID) {
		t.Fatalf("environment=%v", ee.GetEnvironment())
	}
	got := map[string]*commonv1.Variable{}
	for _, v := range ee.GetVariables() {
		got[v.GetKey()] = v
	}
	if len(got) != 3 {
		t.Fatalf("variables should be 3 (global 2 + env 1), got %d", len(got))
	}
	if got["BASE_HOST"].GetValue() != "example.com" {
		t.Fatalf("global var wrong: %+v", got["BASE_HOST"])
	}
	if got["TOKEN"].GetValue() != "t123" {
		t.Fatalf("env var wrong: %+v", got["TOKEN"])
	}
	if sec := got["SECRET"]; !sec.GetSensitive() || sec.GetValue() != "" || sec.GetSecretRef() != "vault://k" {
		t.Fatalf("sensitive var should be redacted (value empty, secret_ref kept): %+v", sec)
	}
	if _, ok := got["OTHER_ENV"]; ok {
		t.Fatal("other env variable leaked into ExecutionEnv")
	}
	if _, ok := got["OTHER_PROJ"]; ok {
		t.Fatal("other project variable leaked into ExecutionEnv")
	}
}

func TestTriggerPlanNotFound(t *testing.T) {
	d := openTestDB(t)
	r := New(d, dispatch.New(d))
	runID, err := r.Trigger(context.Background(), 1, model.NextID(), 0,
		int16(commonv1.TriggerType_TRIGGER_TYPE_MANUAL), "tester")
	if err == nil {
		t.Fatal("should fail")
	}
	if runID != 0 {
		t.Fatalf("runID should be 0, got %d", runID)
	}
	if ae := apperr.From(err); ae.HTTP != 404 || ae.Code != apperr.CodePlanNotFound {
		t.Fatalf("want 404 PLAN_NOT_FOUND, got %+v", ae)
	}
}

func TestTriggerPlanNoItems(t *testing.T) {
	d := openTestDB(t)
	r := New(d, dispatch.New(d))

	bare := model.TestPlan{ID: model.NextID(), TenantID: 1, Name: "empty"}
	if err := d.Create(&bare).Error; err != nil {
		t.Fatal(err)
	}
	disabledPlan := model.TestPlan{ID: model.NextID(), TenantID: 1, Name: "disabled"}
	if err := d.Create(&disabledPlan).Error; err != nil {
		t.Fatal(err)
	}
	item := model.TestPlanItem{ID: model.NextID(), TenantID: 1, PlanID: disabledPlan.ID,
		RefType: 1, RefID: 1, Enabled: false}
	if err := d.Create(&item).Error; err != nil {
		t.Fatal(err)
	}

	for _, planID := range []int64{bare.ID, disabledPlan.ID} {
		runID, err := r.Trigger(context.Background(), 1, planID, 0,
			int16(commonv1.TriggerType_TRIGGER_TYPE_MANUAL), "tester")
		if err == nil {
			t.Fatalf("plan %d should fail", planID)
		}
		if runID != 0 {
			t.Fatalf("plan %d: runID should be 0, got %d", planID, runID)
		}
		if ae := apperr.From(err); ae.HTTP != 400 || ae.Code != apperr.CodePlanNoItems {
			t.Fatalf("plan %d: want 400 PLAN_NO_ITEMS, got %+v", planID, ae)
		}
	}
}

func TestTriggerQuotaExceeded(t *testing.T) {
	d := openTestDB(t)
	r := New(d, dispatch.New(d))

	// 已有一条 RUNNING run，concurrent_runs 上限 1 → 0+1 超限。
	run := model.TestRun{ID: model.NextID(), TenantID: 1, PlanID: 1,
		Status: int16(commonv1.RunStatus_RUN_STATUS_RUNNING), StartedAt: time.Now()}
	if err := d.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	if err := quota.Set(d, 1, quota.MetricConcurrentRuns, 1); err != nil {
		t.Fatal(err)
	}

	runID, err := r.Trigger(context.Background(), 1, model.NextID(), 0,
		int16(commonv1.TriggerType_TRIGGER_TYPE_MANUAL), "tester")
	if err == nil {
		t.Fatal("should fail")
	}
	if runID != 0 {
		t.Fatalf("runID should be 0, got %d", runID)
	}
	if ae := apperr.From(err); ae.HTTP != 429 || ae.Code != apperr.CodeQuotaExceeded {
		t.Fatalf("want 429 QUOTA_EXCEEDED, got %+v", ae)
	}
}

func TestTriggerNoWorker(t *testing.T) {
	d := openTestDB(t)
	fx := seedPlanData(t, d)
	r := New(d, dispatch.New(d)) // 不注册任何 Worker

	runID, err := r.Trigger(context.Background(), 1, fx.planID, 0,
		int16(commonv1.TriggerType_TRIGGER_TYPE_MANUAL), "tester")
	if !errors.Is(err, dispatch.ErrNoWorker) {
		t.Fatalf("want ErrNoWorker, got %v", err)
	}
	if runID == 0 {
		t.Fatal("run should be persisted even when dispatch fails")
	}

	var run model.TestRun
	if err := d.First(&run, runID).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != int16(commonv1.RunStatus_RUN_STATUS_FAILED) {
		t.Fatalf("run status=%d, want FAILED", run.Status)
	}
	if !strings.Contains(string(run.Summary), "no suitable worker") {
		t.Fatalf("summary should mention no suitable worker, got %s", run.Summary)
	}
	if run.FinishedAt == nil {
		t.Fatal("finished_at should be set on failed run")
	}

	var cr model.TestCaseResult
	if err := d.Where("run_id = ?", runID).First(&cr).Error; err != nil {
		t.Fatal(err)
	}
	if cr.Status != int16(commonv1.CaseStatus_CASE_STATUS_FAILED) {
		t.Fatalf("case result status=%d, want FAILED", cr.Status)
	}
	if cr.Error == "" {
		t.Fatal("case result should carry dispatch error")
	}
}

func TestTriggerSuiteRefSkipped(t *testing.T) {
	d := openTestDB(t)
	// plan 只有一个 ref_type=2(suite) 的 enabled item：MVP 未实现 suite 展开，
	// 当前代码路径是 items 非空（不报 PLAN_NO_ITEMS）→ 循环跳过 → dispatched=0 → ErrNoWorker。
	plan := model.TestPlan{ID: model.NextID(), TenantID: 1, Name: "suite-only"}
	if err := d.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	item := model.TestPlanItem{ID: model.NextID(), TenantID: 1, PlanID: plan.ID,
		RefType: 2, RefID: model.NextID(), Enabled: true, Order: 1}
	if err := d.Create(&item).Error; err != nil {
		t.Fatal(err)
	}

	r := New(d, dispatch.New(d))
	runID, err := r.Trigger(context.Background(), 1, plan.ID, 0,
		int16(commonv1.TriggerType_TRIGGER_TYPE_MANUAL), "tester")
	if !errors.Is(err, dispatch.ErrNoWorker) {
		t.Fatalf("suite-only plan: want ErrNoWorker (dispatched=0 path), got %v", err)
	}
	var run model.TestRun
	if err := d.First(&run, runID).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != int16(commonv1.RunStatus_RUN_STATUS_FAILED) {
		t.Fatalf("run status=%d, want FAILED", run.Status)
	}
	var n int64
	d.Model(&model.TestCaseResult{}).Where("run_id = ?", runID).Count(&n)
	if n != 0 {
		t.Fatalf("suite ref should not create case results, got %d", n)
	}
}

func TestBuildExecutionEnv(t *testing.T) {
	vars := []model.Variable{
		{ID: 1, TenantID: 1, ProjectID: 1, Key: "A", Value: "1"},
	}

	// env 不存在：Environment 为 nil，但变量仍然带上。
	ee := buildExecutionEnv(&model.Environment{}, false, vars)
	if ee.Environment != nil {
		t.Fatalf("env not exists: Environment should be nil, got %+v", ee.Environment)
	}
	if ee.BaseUrl != "" {
		t.Fatalf("env not exists: BaseUrl should be empty, got %q", ee.BaseUrl)
	}
	if len(ee.Variables) != 1 || ee.Variables[0].GetKey() != "A" {
		t.Fatalf("variables should survive missing env: %+v", ee.Variables)
	}

	// env 存在：Environment 与 BaseUrl 都有。
	env := &model.Environment{ID: 9, TenantID: 1, ProjectID: 1, Name: "prod", BaseURL: "https://prod.example.com"}
	ee = buildExecutionEnv(env, true, vars)
	if ee.GetEnvironment().GetId() != "9" || ee.GetBaseUrl() != "https://prod.example.com" {
		t.Fatalf("env exists: %+v", ee)
	}

	// 无变量：Variables 为空。
	ee = buildExecutionEnv(env, true, nil)
	if len(ee.Variables) != 0 {
		t.Fatalf("want no variables, got %+v", ee.Variables)
	}
}
