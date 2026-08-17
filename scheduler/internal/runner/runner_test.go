package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
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
	"google.golang.org/protobuf/types/known/structpb"
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

// seedSuite 建一个 tenant 1 下含 N 个 case 的套件。
func seedSuite(t *testing.T, d *gorm.DB, caseIDs ...int64) int64 {
	t.Helper()
	su := model.TestSuite{ID: model.NextID(), TenantID: 1, ProjectID: 1, Name: "suite"}
	if err := d.Create(&su).Error; err != nil {
		t.Fatal(err)
	}
	for i, cid := range caseIDs {
		it := model.TestSuiteItem{ID: model.NextID(), SuiteID: su.ID, CaseID: cid, Order: i}
		if err := d.Create(&it).Error; err != nil {
			t.Fatal(err)
		}
	}
	return su.ID
}

func TestTriggerSuiteExpansion(t *testing.T) {
	d := openTestDB(t)
	fx := seedPlanData(t, d)
	// 套件内两个 case（顺序 A, B），plan 改为引用该套件
	caseB := model.TestCase{
		ID: model.NextID(), TenantID: 1, ProjectID: fx.projID,
		Type: int16(commonv1.TestCaseType_TEST_CASE_TYPE_DECLARATIVE),
		Name: "second", Definition: model.JSON(`{}`),
	}
	if err := d.Create(&caseB).Error; err != nil {
		t.Fatal(err)
	}
	suiteID := seedSuite(t, d, fx.caseID, caseB.ID)
	d.Model(&model.TestPlanItem{}).Where("plan_id = ?", fx.planID).
		Updates(map[string]any{"ref_type": 2, "ref_id": suiteID})

	disp := dispatch.New(d)
	disp.Register(mkWorker("w1", 0, commonv1.Capability_CAPABILITY_FUNCTIONAL))
	r := New(d, disp)
	runID, err := r.Trigger(context.Background(), 1, fx.planID, 0,
		int16(commonv1.TriggerType_TRIGGER_TYPE_MANUAL), "tester")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}

	var crs []model.TestCaseResult
	d.Where("run_id = ?", runID).Order("id asc").Find(&crs)
	if len(crs) != 2 {
		t.Fatalf("want 2 case results from suite expansion, got %d", len(crs))
	}
	if crs[0].CaseID != fx.caseID || crs[1].CaseID != caseB.ID {
		t.Fatalf("case results out of suite order: %d, %d", crs[0].CaseID, crs[1].CaseID)
	}

	// 展开的 case 应与直接引用一样被派发（worker 收到 2 个任务）
	var w *dispatch.Worker
	for _, x := range disp.Workers() {
		if x.ID == "w1" {
			w = x
		}
	}
	if w == nil || len(w.Send) != 2 {
		t.Fatalf("want 2 dispatched tasks on w1, got %+v", w)
	}
}

func TestTriggerSuiteMissing(t *testing.T) {
	d := openTestDB(t)
	// ref_type=2 指向不存在的套件：展开失败 → 跳过 → dispatched=0 → ErrNoWorker。
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
		t.Fatalf("missing suite: want ErrNoWorker (dispatched=0 path), got %v", err)
	}
	var n int64
	d.Model(&model.TestCaseResult{}).Where("run_id = ?", runID).Count(&n)
	if n != 0 {
		t.Fatalf("missing suite should not create case results, got %d", n)
	}
}

func TestMaterializeScriptRef(t *testing.T) {
	d := openTestDB(t)
	r := New(d, dispatch.New(d))

	script := model.Script{ID: model.NextID(), TenantID: 1, ProjectID: 1,
		Name: "shared-flow", Language: "python", Content: "def run(ctx):\n    return True"}
	if err := d.Create(&script).Error; err != nil {
		t.Fatal(err)
	}

	// script_ref 用例 → 内联 source，ref 清空，entry 保留
	lc := model.TestCase{
		ID: model.NextID(), TenantID: 1, ProjectID: 1,
		Type:       int16(commonv1.TestCaseType_TEST_CASE_TYPE_LOWCODE),
		Name:       "ref-case",
		Definition: model.JSON(`{"script_ref": "` + idStr(script.ID) + `", "entry": "run"}`),
	}
	pcase, _, _, err := r.materializeCase(&lc)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if got := pcase.GetLowcode().GetSource(); got != script.Content {
		t.Fatalf("source not inlined, got %q", got)
	}
	if got := pcase.GetLowcode().GetScriptRef(); got != "" {
		t.Fatalf("script_ref should be cleared, got %q", got)
	}
	if got := pcase.GetLowcode().GetEntry(); got != "run" {
		t.Fatalf("entry should be preserved, got %q", got)
	}

	// 脚本不存在 → 报错（派发侧将 case result 置 FAILED）
	missing := model.TestCase{
		ID: model.NextID(), TenantID: 1, ProjectID: 1,
		Type:       int16(commonv1.TestCaseType_TEST_CASE_TYPE_LOWCODE),
		Name:       "missing",
		Definition: model.JSON(`{"script_ref": "999999"}`),
	}
	if _, _, _, err := r.materializeCase(&missing); err == nil {
		t.Fatal("missing script should error")
	}

	// 跨租户脚本不可见
	foreign := model.Script{ID: model.NextID(), TenantID: 2, ProjectID: 1,
		Name: "foreign", Content: "def run(ctx): pass"}
	if err := d.Create(&foreign).Error; err != nil {
		t.Fatal(err)
	}
	foreignCase := model.TestCase{
		ID: model.NextID(), TenantID: 1, ProjectID: 1,
		Type:       int16(commonv1.TestCaseType_TEST_CASE_TYPE_LOWCODE),
		Name:       "foreign-ref",
		Definition: model.JSON(`{"script_ref": "` + idStr(foreign.ID) + `"}`),
	}
	if _, _, _, err := r.materializeCase(&foreignCase); err == nil {
		t.Fatal("cross-tenant script_ref should error")
	}

	// 非低代码 / source 用例不受影响
	decl := model.TestCase{
		ID: model.NextID(), TenantID: 1, ProjectID: 1,
		Type: int16(commonv1.TestCaseType_TEST_CASE_TYPE_DECLARATIVE),
		Name: "decl", Definition: model.JSON(`{"steps": []}`),
	}
	if _, _, _, err := r.materializeCase(&decl); err != nil {
		t.Fatalf("declarative should pass through: %v", err)
	}
}

func TestMaterializeAPIRefs(t *testing.T) {
	d := openTestDB(t)
	r := New(d, dispatch.New(d))

	api := model.HttpApi{ID: model.NextID(), TenantID: 1, ProjectID: 1,
		Method: int16(commonv1.HttpMethod_HTTP_METHOD_GET), URI: "/echo",
		Params: model.JSON(`[{"key":"page","value":""}]`)}
	if err := d.Create(&api).Error; err != nil {
		t.Fatal(err)
	}

	// 顶层 + loop 嵌套 + override：inline 解析、api_id/override 保留
	decl := model.TestCase{
		ID: model.NextID(), TenantID: 1, ProjectID: 1,
		Type: int16(commonv1.TestCaseType_TEST_CASE_TYPE_DECLARATIVE),
		Name: "api-ref",
		Definition: model.JSON(`{"steps": [
			{"name": "top", "type": 1, "api_call": {"api_id": "` + idStr(api.ID) + `", "override": {"params": [{"key": "page", "value": "2"}]}}},
			{"name": "loop", "type": 6, "loop_step": {"iterator": "i", "count": 1, "body_steps": [
				{"name": "nested", "type": 1, "api_call": {"api_id": "` + idStr(api.ID) + `"}}]}}
		]}`),
	}
	pcase, _, _, err := r.materializeCase(&decl)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	steps := pcase.GetDeclarative().GetSteps()
	top := steps[0].GetApiCall()
	if top.GetApiId() != idStr(api.ID) {
		t.Fatalf("api_id should be preserved, got %q", top.GetApiId())
	}
	if top.Inline == nil || top.Inline.GetUri() != "/echo" ||
		top.Inline.GetMethod() != commonv1.HttpMethod_HTTP_METHOD_GET {
		t.Fatalf("inline not resolved: %+v", top.Inline)
	}
	if len(top.Inline.GetParams()) != 1 || top.Inline.GetParams()[0].GetKey() != "page" {
		t.Fatalf("inline params mismatch: %+v", top.Inline.GetParams())
	}
	if len(top.GetOverride().GetParams()) != 1 {
		t.Fatal("override should be preserved")
	}
	nested := steps[1].GetLoopStep().GetBodySteps()[0].GetApiCall()
	if nested.Inline == nil {
		t.Fatal("nested api_id should be resolved")
	}

	// 已有 inline 的步骤不覆盖
	own := model.TestCase{
		ID: model.NextID(), TenantID: 1, ProjectID: 1,
		Type: int16(commonv1.TestCaseType_TEST_CASE_TYPE_DECLARATIVE),
		Name: "inline-own",
		Definition: model.JSON(`{"steps": [
			{"name": "own", "type": 1, "api_call": {"api_id": "` + idStr(api.ID) + `", "inline": {"method": 2, "uri": "/mine"}}}
		]}`),
	}
	p2, _, _, err := r.materializeCase(&own)
	if err != nil {
		t.Fatal(err)
	}
	if got := p2.GetDeclarative().GetSteps()[0].GetApiCall().Inline.GetUri(); got != "/mine" {
		t.Fatalf("explicit inline should win, got %q", got)
	}

	// 缺失接口 / 非法 id → 报错
	missing := model.TestCase{
		ID: model.NextID(), TenantID: 1, ProjectID: 1,
		Type: int16(commonv1.TestCaseType_TEST_CASE_TYPE_DECLARATIVE),
		Name: "missing-api",
		Definition: model.JSON(`{"steps": [
			{"name": "x", "type": 1, "api_call": {"api_id": "999999"}}
		]}`),
	}
	if _, _, _, err := r.materializeCase(&missing); err == nil {
		t.Fatal("missing api should error")
	}
	badID := model.TestCase{
		ID: model.NextID(), TenantID: 1, ProjectID: 1,
		Type: int16(commonv1.TestCaseType_TEST_CASE_TYPE_DECLARATIVE),
		Name: "bad-id",
		Definition: model.JSON(`{"steps": [
			{"name": "x", "type": 1, "api_call": {"api_id": "not-a-number"}}
		]}`),
	}
	if _, _, _, err := r.materializeCase(&badID); err == nil {
		t.Fatal("invalid api_id should error")
	}

	// 跨租户接口不可见
	foreign := model.HttpApi{ID: model.NextID(), TenantID: 2, ProjectID: 1,
		Method: int16(commonv1.HttpMethod_HTTP_METHOD_GET), URI: "/foreign"}
	if err := d.Create(&foreign).Error; err != nil {
		t.Fatal(err)
	}
	foreignCase := model.TestCase{
		ID: model.NextID(), TenantID: 1, ProjectID: 1,
		Type: int16(commonv1.TestCaseType_TEST_CASE_TYPE_DECLARATIVE),
		Name: "foreign-api",
		Definition: model.JSON(`{"steps": [
			{"name": "x", "type": 1, "api_call": {"api_id": "` + idStr(foreign.ID) + `"}}
		]}`),
	}
	if _, _, _, err := r.materializeCase(&foreignCase); err == nil {
		t.Fatal("cross-tenant api_id should error")
	}
}

func TestMaterializeGrpcRefs(t *testing.T) {
	d := openTestDB(t)
	r := New(d, dispatch.New(d))

	api := model.GrpcApi{ID: model.NextID(), TenantID: 1, ProjectID: 1,
		FullService: "testpilot.echo.v1.EchoService", Method: "Echo",
		RequestMessage: model.JSON(`{"message":"hi"}`), DeadlineMs: 5000}
	if err := d.Create(&api).Error; err != nil {
		t.Fatal(err)
	}

	decl := model.TestCase{
		ID: model.NextID(), TenantID: 1, ProjectID: 1,
		Type: int16(commonv1.TestCaseType_TEST_CASE_TYPE_DECLARATIVE),
		Name: "grpc-ref",
		Definition: model.JSON(`{"steps": [
			{"name": "call", "grpc_call": {"grpc_api_id": "` + idStr(api.ID) + `"}},
			{"name": "loop", "loop_step": {"iterator": "i", "count": 1, "body_steps": [
				{"name": "nested", "grpc_call": {"grpc_api_id": "` + idStr(api.ID) + `"}}]}}
		]}`),
	}
	pcase, grpcAPIs, _, err := r.materializeCase(&decl)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if len(grpcAPIs) != 1 {
		t.Fatalf("want 1 grpc api, got %d", len(grpcAPIs))
	}
	got, ok := grpcAPIs[idStr(api.ID)]
	if !ok || got.GetFullService() != "testpilot.echo.v1.EchoService" ||
		got.GetMethod() != "Echo" {
		t.Fatalf("grpc api map wrong: %+v", grpcAPIs)
	}
	if got.GetDeadline().AsDuration().Milliseconds() != 5000 {
		t.Fatalf("deadline not converted: %v", got.GetDeadline())
	}
	if got.GetRequestMessage() == nil || got.GetRequestMessage().Fields["message"].GetStringValue() != "hi" {
		t.Fatalf("request_message not converted: %+v", got.GetRequestMessage())
	}
	_ = pcase

	// 缺失 / 非法 id / 跨租户 → 报错
	for name, def := range map[string]string{
		"missing":  `{"steps": [{"name": "x", "grpc_call": {"grpc_api_id": "999999"}}]}`,
		"bad-id":   `{"steps": [{"name": "x", "grpc_call": {"grpc_api_id": "nope"}}]}`,
		"cross-tn": `{"steps": [{"name": "x", "grpc_call": {"grpc_api_id": "` + idStr(api.ID+1) + `"}}]}`,
	} {
		tc := model.TestCase{
			ID: model.NextID(), TenantID: 1, ProjectID: 1,
			Type: int16(commonv1.TestCaseType_TEST_CASE_TYPE_DECLARATIVE),
			Name: name, Definition: model.JSON(def),
		}
		if _, _, _, err := r.materializeCase(&tc); err == nil {
			t.Fatalf("%s: should error", name)
		}
	}
}

func TestApplyOverrides(t *testing.T) {
	baseEnv := &workerv1.ExecutionEnv{BaseUrl: "http://b"}
	baseEnv.Variables = append(baseEnv.Variables, &commonv1.Variable{Key: "n", Value: "1"})

	// 声明式：覆盖追加为任务级模板变量（排在 env 变量之后，取末值优先）
	decl := &commonv1.TestCase{Definition: &commonv1.TestCase_Declarative{
		Declarative: &commonv1.DeclarativeCase{}}}
	env, err := applyOverrides(model.JSON(`{"n": 7, "flag": true, "obj": {"a": 1}}`), decl, baseEnv)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.GetVariables()) != 4 {
		t.Fatalf("vars=%v", env.GetVariables())
	}
	// override 按 key 排序输出（确定性）：env[n=1] + overrides[flag=true, n=7, obj={a:1}]
	if env.GetVariables()[0].GetValue() != "1" || env.GetVariables()[1].GetValue() != "true" {
		t.Fatalf("override should append after env vars: %+v", env.GetVariables())
	}
	if env.GetVariables()[2].GetValue() != "7" || env.GetVariables()[3].GetValue() != `{"a":1}` {
		t.Fatalf("non-string overrides: %+v", env.GetVariables()[2:])
	}
	if env.GetBaseUrl() != "http://b" {
		t.Fatal("base_url should be preserved")
	}
	// 共享 execEnv 未被修改
	if len(baseEnv.GetVariables()) != 1 {
		t.Fatal("shared execEnv must not be mutated")
	}

	// 低代码：深合并进 parameters（覆盖优先），同时追加模板变量
	lc := &commonv1.TestCase{Definition: &commonv1.TestCase_Lowcode{
		Lowcode: &commonv1.LowCodeCase{}}}
	lc.GetLowcode().Parameters, _ = structpb.NewStruct(map[string]any{"x": 1.0, "y": map[string]any{"a": 1.0}})
	_, err = applyOverrides(model.JSON(`{"y": {"b": 2}, "z": "v"}`), lc, baseEnv)
	if err != nil {
		t.Fatal(err)
	}
	got := lc.GetLowcode().GetParameters().AsMap()
	if got["x"] != 1.0 || got["z"] != "v" {
		t.Fatalf("parameters merge mismatch: %v", got)
	}
	nested := got["y"].(map[string]any)
	if nested["a"] != 1.0 || nested["b"] != 2.0 {
		t.Fatalf("nested merge mismatch: %v", nested)
	}

	// 非法 JSON → 报错；空覆盖原样返回
	if _, err := applyOverrides(model.JSON(`{bad`), decl, baseEnv); err == nil {
		t.Fatal("invalid json should error")
	}
	if env, _ := applyOverrides(nil, decl, baseEnv); env != baseEnv {
		t.Fatal("empty overrides should return env unchanged")
	}
}

func TestTriggerParamOverridesDispatched(t *testing.T) {
	d := openTestDB(t)
	fx := seedPlanData(t, d)
	d.Model(&model.TestPlanItem{}).Where("plan_id = ?", fx.planID).
		Updates(map[string]any{"param_overrides": model.JSON(`{"page": "42"}`)})

	disp := dispatch.New(d)
	disp.Register(mkWorker("w1", 0, commonv1.Capability_CAPABILITY_FUNCTIONAL))
	r := New(d, disp)
	if _, err := r.Trigger(context.Background(), 1, fx.planID, 0,
		int16(commonv1.TriggerType_TRIGGER_TYPE_MANUAL), "tester"); err != nil {
		t.Fatal(err)
	}
	var w *dispatch.Worker
	for _, x := range disp.Workers() {
		if x.ID == "w1" {
			w = x
		}
	}
	if w == nil || len(w.Send) != 1 {
		t.Fatalf("want 1 dispatched task, got %+v", w)
	}
	task := (<-w.Send).GetTask()
	vars := task.GetEnv().GetVariables()
	if len(vars) == 0 || vars[len(vars)-1].GetKey() != "page" ||
		vars[len(vars)-1].GetValue() != "42" {
		t.Fatalf("override var missing at tail: %+v", vars)
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

func TestArtifactIDFromRef(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		err  bool
	}{
		{"artifact:123", 123, false},
		{"123", 123, false},
		{"base64:aGk=", 0, false},
		{"artifact:", 0, true},
		{"nope", 0, true},
	}
	for _, c := range cases {
		got, err := artifactIDFromRef(c.in)
		if got != c.want || (err != nil) != c.err {
			t.Fatalf("artifactIDFromRef(%q)=%d,%v want %d,err=%v", c.in, got, err, c.want, c.err)
		}
	}
}

func TestResolveInlineFilesArtifactRef(t *testing.T) {
	d := openTestDB(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "body.bin"), []byte("artifact-hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	art := model.Artifact{ID: model.NextID(), TenantID: 1, RunID: 1, Kind: model.ArtifactKindCert, URI: "body.bin", Size: 14}
	if err := d.Create(&art).Error; err != nil {
		t.Fatal(err)
	}
	disp := dispatch.New(d)
	disp.SetArtifactIngest(nil, root)
	r := New(d, disp)

	pcase := &commonv1.TestCase{
		Definition: &commonv1.TestCase_Declarative{Declarative: &commonv1.DeclarativeCase{
			Steps: []*commonv1.TestStep{{
				Params: &commonv1.TestStep_ApiCall{ApiCall: &commonv1.ApiCallStep{
					Inline: &commonv1.HttpApi{Body: &commonv1.BodySpec{
						ContentType: commonv1.BodyContentType_BODY_CONTENT_TYPE_BINARY,
						Content:     &commonv1.BodySpec_BinaryRef{BinaryRef: "artifact:" + fmt.Sprint(art.ID)},
					}},
				}},
			}},
		}},
	}
	files, err := r.resolveInlineFiles(1, pcase)
	if err != nil {
		t.Fatal(err)
	}
	if string(files["artifact:"+fmt.Sprint(art.ID)]) != "artifact-hello" {
		t.Fatalf("inline file mismatch: %q", files["artifact:"+fmt.Sprint(art.ID)])
	}
}
