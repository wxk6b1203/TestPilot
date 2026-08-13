package cronsched

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"

	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	"github.com/testpilot/testpilot/internal/db"
	"github.com/testpilot/testpilot/internal/dispatch"
	"github.com/testpilot/testpilot/internal/model"
	"github.com/testpilot/testpilot/internal/runner"
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

// seedPlan 建 tenant 1 下的 plan + 1 个 enabled case item（无 env，fire 后 Trigger 走无变量路径）。
func seedPlan(t *testing.T, d *gorm.DB) int64 {
	t.Helper()
	proj := model.Project{ID: model.NextID(), TenantID: 1, Name: "proj"}
	if err := d.Create(&proj).Error; err != nil {
		t.Fatal(err)
	}
	tc := model.TestCase{
		ID: model.NextID(), TenantID: 1, ProjectID: proj.ID,
		Type: int16(commonv1.TestCaseType_TEST_CASE_TYPE_DECLARATIVE),
		Name: "c", Definition: model.JSON(`{}`),
	}
	if err := d.Create(&tc).Error; err != nil {
		t.Fatal(err)
	}
	plan := model.TestPlan{ID: model.NextID(), TenantID: 1, ProjectID: proj.ID, Name: "plan"}
	if err := d.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	item := model.TestPlanItem{ID: model.NextID(), TenantID: 1, PlanID: plan.ID,
		RefType: 1, RefID: tc.ID, Enabled: true, Order: 1}
	if err := d.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	return plan.ID
}

func countRuns(t *testing.T, d *gorm.DB, planID int64) int64 {
	t.Helper()
	var n int64
	d.Model(&model.TestRun{}).Where("plan_id = ?", planID).Count(&n)
	return n
}

func TestNextOf(t *testing.T) {
	s := New(nil, nil) // nextOf 不触库
	now := time.Now()

	next := s.nextOf("*/5 * * * *")
	if next == nil {
		t.Fatal("valid expr should return non-nil")
	}
	if !next.After(now) {
		t.Fatalf("next %v should be after now %v", next, now)
	}
	if next.After(now.Add(5*time.Minute + time.Second)) {
		t.Fatalf("*/5 next %v should be within 5min", next)
	}

	jan1 := s.nextOf("0 0 1 1 *")
	if jan1 == nil {
		t.Fatal("valid expr should return non-nil")
	}
	if jan1.Month() != time.January || jan1.Day() != 1 || jan1.Hour() != 0 || jan1.Minute() != 0 {
		t.Fatalf("0 0 1 1 * → %v", jan1)
	}

	for _, bad := range []string{"bogus", "*/5 * * *", "* * * * * *", "61 * * * *"} {
		if got := s.nextOf(bad); got != nil {
			t.Fatalf("expr %q should be invalid, got %v", bad, got)
		}
	}
}

func TestSync(t *testing.T) {
	d := openTestDB(t)
	s := New(d, runner.New(d, dispatch.New(d)))
	t.Cleanup(s.Stop) // 未 Start 也安全

	sc := &model.Schedule{ID: model.NextID(), TenantID: 1, PlanID: 1,
		CronExpr: "*/5 * * * *", Enabled: true}
	if err := d.Create(sc).Error; err != nil {
		t.Fatal(err)
	}

	// enabled + 合法表达式 → 加入条目，next_run_at 落库。
	s.Sync(sc)
	if len(s.entries) != 1 {
		t.Fatalf("entries=%d", len(s.entries))
	}
	eid1 := s.entries[sc.ID]
	var got model.Schedule
	if err := d.First(&got, sc.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.NextRunAt == nil || !got.NextRunAt.After(time.Now()) {
		t.Fatalf("next_run_at should be persisted in future, got %v", got.NextRunAt)
	}

	// 同 ID 更新表达式 → 旧条目被替换，next_run_at 重算。
	sc.CronExpr = "0 0 1 1 *"
	s.Sync(sc)
	if len(s.entries) != 1 {
		t.Fatalf("after update entries=%d", len(s.entries))
	}
	if s.entries[sc.ID] == eid1 {
		t.Fatal("old cron entry should be replaced")
	}
	if err := d.First(&got, sc.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.NextRunAt == nil || got.NextRunAt.Month() != time.January || got.NextRunAt.Day() != 1 {
		t.Fatalf("next_run_at should be recomputed to Jan 1, got %v", got.NextRunAt)
	}

	// Enabled=false → 条目移除。
	sc.Enabled = false
	s.Sync(sc)
	if len(s.entries) != 0 {
		t.Fatalf("disabled schedule should be removed, entries=%d", len(s.entries))
	}

	// 非法表达式 → 不加且不崩。
	bad := &model.Schedule{ID: model.NextID(), TenantID: 1, PlanID: 1,
		CronExpr: "bogus", Enabled: true}
	if err := d.Create(bad).Error; err != nil {
		t.Fatal(err)
	}
	s.Sync(bad)
	if len(s.entries) != 0 {
		t.Fatalf("invalid expr should not be added, entries=%d", len(s.entries))
	}
}

func TestRemove(t *testing.T) {
	d := openTestDB(t)
	s := New(d, runner.New(d, dispatch.New(d)))
	t.Cleanup(s.Stop)

	sc := &model.Schedule{ID: model.NextID(), TenantID: 1, PlanID: 1,
		CronExpr: "*/5 * * * *", Enabled: true}
	if err := d.Create(sc).Error; err != nil {
		t.Fatal(err)
	}
	s.Sync(sc)
	if len(s.entries) != 1 {
		t.Fatalf("entries=%d", len(s.entries))
	}
	s.Remove(sc.ID)
	if len(s.entries) != 0 {
		t.Fatalf("after remove entries=%d", len(s.entries))
	}
	s.Remove(model.NextID()) // 不存在的 ID：无操作不崩
}

func TestStartMisfireCatchUp(t *testing.T) {
	d := openTestDB(t)
	planID := seedPlan(t, d)
	now := time.Now()
	past := now.Add(-10 * time.Minute) // 超过 2min 宽限 → misfire
	future := now.Add(1 * time.Hour)

	scMisfire := &model.Schedule{ID: model.NextID(), TenantID: 1, PlanID: planID,
		CronExpr: "0 0 1 1 *", Enabled: true, NextRunAt: &past}
	scFuture := &model.Schedule{ID: model.NextID(), TenantID: 1, PlanID: planID,
		CronExpr: "0 0 1 1 *", Enabled: true, NextRunAt: &future}
	scDisabled := &model.Schedule{ID: model.NextID(), TenantID: 1, PlanID: planID,
		CronExpr: "0 0 1 1 *", Enabled: false, NextRunAt: &past}
	for _, sc := range []*model.Schedule{scMisfire, scFuture, scDisabled} {
		if err := d.Create(sc).Error; err != nil {
			t.Fatal(err)
		}
	}

	// 无 Worker：fire 后 run 会 FAILED，但行存在即证明补跑发生。
	s := New(d, runner.New(d, dispatch.New(d)))
	s.Start() // misfire 补跑在 Start 内同步执行
	t.Cleanup(s.Stop)

	byMisfire := "schedule:" + strconv.FormatInt(scMisfire.ID, 10)
	var run model.TestRun
	if err := d.Where("triggered_by = ?", byMisfire).First(&run).Error; err != nil {
		t.Fatalf("misfire schedule should have fired one run: %v", err)
	}
	if run.Trigger != int16(commonv1.TriggerType_TRIGGER_TYPE_SCHEDULED) {
		t.Fatalf("run trigger=%d, want SCHEDULED", run.Trigger)
	}
	if run.PlanID != planID || run.TenantID != 1 {
		t.Fatalf("run plan/tenant wrong: %+v", run)
	}
	if run.Status != int16(commonv1.RunStatus_RUN_STATUS_FAILED) {
		t.Fatalf("no worker → run should be FAILED, got %d", run.Status)
	}
	if n := countRuns(t, d, planID); n != 1 {
		t.Fatalf("exactly one catch-up run expected, got %d", n)
	}

	// NextRunAt=now+1h 不补跑；disabled 不加载也不补跑。
	for _, sc := range []*model.Schedule{scFuture, scDisabled} {
		by := "schedule:" + strconv.FormatInt(sc.ID, 10)
		var n int64
		d.Model(&model.TestRun{}).Where("triggered_by = ?", by).Count(&n)
		if n != 0 {
			t.Fatalf("schedule %d should not have fired, got %d runs", sc.ID, n)
		}
	}

	// misfire 的 schedule last_run_at 被 touch；next_run_at 已被 add 重算为未来。
	var got model.Schedule
	if err := d.First(&got, scMisfire.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.LastRunAt == nil {
		t.Fatal("misfire schedule last_run_at should be touched")
	}
	if got.NextRunAt == nil || !got.NextRunAt.After(now) {
		t.Fatalf("next_run_at should be recomputed to future, got %v", got.NextRunAt)
	}
	// 注意：复用 got 前必须清零，否则 GORM 会把已填充的主键拼进 WHERE（id=X AND id=Y 永不命中）。
	got = model.Schedule{}
	if err := d.First(&got, scFuture.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.LastRunAt != nil {
		t.Fatalf("future schedule should not be touched, last_run_at=%v", got.LastRunAt)
	}
}

func TestFireOverlap(t *testing.T) {
	d := openTestDB(t)
	planID := seedPlan(t, d)
	s := New(d, runner.New(d, dispatch.New(d))) // 不 Start，直接调私有 fire

	sc := &model.Schedule{ID: model.NextID(), TenantID: 1, PlanID: planID,
		CronExpr: "0 0 1 1 *", OverlapPolicy: 1, Enabled: true}
	if err := d.Create(sc).Error; err != nil {
		t.Fatal(err)
	}

	// 该 plan 已有一条 RUNNING run。
	run0 := model.TestRun{ID: model.NextID(), TenantID: 1, PlanID: planID,
		Status: int16(commonv1.RunStatus_RUN_STATUS_RUNNING), StartedAt: time.Now()}
	if err := d.Create(&run0).Error; err != nil {
		t.Fatal(err)
	}

	// overlap_policy=1（!=2）：跳过新建，但 last_run_at 仍被 touch。
	s.fire(sc.ID)
	if n := countRuns(t, d, planID); n != 1 {
		t.Fatalf("overlap policy 1 should skip, runs=%d", n)
	}
	var got model.Schedule
	if err := d.First(&got, sc.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.LastRunAt == nil {
		t.Fatal("skipped fire should still touch last_run_at")
	}
	if got.NextRunAt == nil {
		t.Fatal("skipped fire should still refresh next_run_at")
	}

	// overlap_policy=2：允许并发，新建 run（无 Worker → FAILED）。
	if err := d.Model(&model.Schedule{}).Where("id = ?", sc.ID).
		Update("overlap_policy", 2).Error; err != nil {
		t.Fatal(err)
	}
	s.fire(sc.ID)
	if n := countRuns(t, d, planID); n != 2 {
		t.Fatalf("overlap policy 2 should fire anyway, runs=%d", n)
	}
	by := "schedule:" + strconv.FormatInt(sc.ID, 10)
	var fired model.TestRun
	if err := d.Where("triggered_by = ?", by).First(&fired).Error; err != nil {
		t.Fatalf("overlap 2 run missing: %v", err)
	}
	if fired.Trigger != int16(commonv1.TriggerType_TRIGGER_TYPE_SCHEDULED) ||
		fired.Status != int16(commonv1.RunStatus_RUN_STATUS_FAILED) {
		t.Fatalf("fired run wrong: trigger=%d status=%d", fired.Trigger, fired.Status)
	}
}
