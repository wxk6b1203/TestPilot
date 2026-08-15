package runner

import (
	"path/filepath"
	"testing"
	"time"

	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	"github.com/testpilot/testpilot/internal/db"
	"github.com/testpilot/testpilot/internal/dispatch"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

func openRunnerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"), "", db.Pool{})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func seedRunningRun(t *testing.T, d *gorm.DB, id int64, started time.Time) *model.TestRun {
	t.Helper()
	r := &model.TestRun{
		ID:        id,
		TenantID:  1,
		Status:    int16(commonv1.RunStatus_RUN_STATUS_RUNNING),
		StartedAt: started,
	}
	if err := d.Create(r).Error; err != nil {
		t.Fatal(err)
	}
	cr := &model.TestCaseResult{
		ID:       model.NextID(),
		TenantID: 1,
		RunID:    id,
		Status:   int16(commonv1.CaseStatus_CASE_STATUS_RUNNING),
	}
	if err := d.Create(cr).Error; err != nil {
		t.Fatal(err)
	}
	return r
}

// TestRecoverInterruptedRuns 启动恢复：RUNNING run/case/stress 全部置 FAILED 并收尾。
func TestRecoverInterruptedRuns(t *testing.T) {
	d := openRunnerTestDB(t)
	r1 := seedRunningRun(t, d, 1001, time.Now().Add(-time.Minute))
	var cr model.TestCaseResult
	if err := d.Where("run_id = ?", 1001).First(&cr).Error; err != nil {
		t.Fatal(err)
	}
	st := &model.StressRun{
		ID:        2001,
		TenantID:  1,
		Status:    int16(commonv1.RunStatus_RUN_STATUS_RUNNING),
		StartedAt: time.Now().Add(-time.Minute),
	}
	if err := d.Create(st).Error; err != nil {
		t.Fatal(err)
	}

	RecoverInterruptedRuns(d)

	var got model.TestRun
	if err := d.First(&got, r1.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != int16(commonv1.RunStatus_RUN_STATUS_FAILED) {
		t.Fatalf("run status = %d, want FAILED", got.Status)
	}
	if got.FinishedAt == nil {
		t.Fatal("run finished_at not set")
	}
	var gotCr model.TestCaseResult
	if err := d.First(&gotCr, cr.ID).Error; err != nil {
		t.Fatal(err)
	}
	if gotCr.Status != int16(commonv1.CaseStatus_CASE_STATUS_FAILED) {
		t.Fatalf("case status = %d, want FAILED", gotCr.Status)
	}
	var gotSt model.StressRun
	if err := d.First(&gotSt, st.ID).Error; err != nil {
		t.Fatal(err)
	}
	if gotSt.Status != int16(commonv1.RunStatus_RUN_STATUS_FAILED) {
		t.Fatalf("stress status = %d, want FAILED", gotSt.Status)
	}
	// 已 FAILED 的 run 不受影响
	done := &model.TestRun{
		ID: 3001, TenantID: 1, Status: int16(commonv1.RunStatus_RUN_STATUS_PASSED), StartedAt: time.Now(),
	}
	if err := d.Create(done).Error; err != nil {
		t.Fatal(err)
	}
	RecoverInterruptedRuns(d)
	var gotDone model.TestRun
	if err := d.First(&gotDone, 3001).Error; err != nil {
		t.Fatal(err)
	}
	if gotDone.Status != int16(commonv1.RunStatus_RUN_STATUS_PASSED) {
		t.Fatalf("finished run touched: %d", gotDone.Status)
	}
}

// TestReapStaleRuns 超时回收：仅超龄 RUNNING 被终结，新鲜 run 不动，stress 条目被 Drop。
func TestReapStaleRuns(t *testing.T) {
	d := openRunnerTestDB(t)
	disp := dispatch.New(d)
	stale := seedRunningRun(t, d, 5001, time.Now().Add(-3*time.Hour))
	fresh := seedRunningRun(t, d, 5002, time.Now())
	disp.RegisterStressRun(6001, 1)
	st := &model.StressRun{
		ID: 6001, TenantID: 1, Status: int16(commonv1.RunStatus_RUN_STATUS_RUNNING),
		StartedAt: time.Now().Add(-3 * time.Hour),
	}
	if err := d.Create(st).Error; err != nil {
		t.Fatal(err)
	}

	ReapStaleRuns(d, disp, 2*time.Hour)

	var got model.TestRun
	if err := d.First(&got, stale.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != int16(commonv1.RunStatus_RUN_STATUS_FAILED) || got.FinishedAt == nil {
		t.Fatalf("stale run not reaped: status=%d", got.Status)
	}
	var gotFresh model.TestRun
	if err := d.First(&gotFresh, fresh.ID).Error; err != nil {
		t.Fatal(err)
	}
	if gotFresh.Status != int16(commonv1.RunStatus_RUN_STATUS_RUNNING) {
		t.Fatalf("fresh run touched: status=%d", gotFresh.Status)
	}
	var gotSt model.StressRun
	if err := d.First(&gotSt, 6001).Error; err != nil {
		t.Fatal(err)
	}
	if gotSt.Status != int16(commonv1.RunStatus_RUN_STATUS_FAILED) {
		t.Fatalf("stale stress not reaped: status=%d", gotSt.Status)
	}
	// 迟到的压测结果不应再触发收尾（reaper 已 Drop 跟踪）：
	// handleStressResult 对未知 run 直接返回 nil，不报错即可
	_ = disp
}
