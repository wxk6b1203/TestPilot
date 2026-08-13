package quota

import (
	"path/filepath"
	"testing"
	"time"

	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	"github.com/testpilot/testpilot/internal/apperr"
	"github.com/testpilot/testpilot/internal/db"
	"github.com/testpilot/testpilot/internal/model"
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

func TestSetAndLimit(t *testing.T) {
	d := openTestDB(t)
	if got := Limit(d, 1, MetricMonthlyRuns); got != 0 {
		t.Fatalf("no row should mean unlimited(0), got %d", got)
	}
	if err := Set(d, 1, MetricMonthlyRuns, 5); err != nil {
		t.Fatal(err)
	}
	if got := Limit(d, 1, MetricMonthlyRuns); got != 5 {
		t.Fatalf("got %d", got)
	}
	// upsert 更新同一条
	if err := Set(d, 1, MetricMonthlyRuns, 9); err != nil {
		t.Fatal(err)
	}
	if got := Limit(d, 1, MetricMonthlyRuns); got != 9 {
		t.Fatalf("upsert got %d", got)
	}
	// limit<=0 删除记录 = 不限
	if err := Set(d, 1, MetricMonthlyRuns, 0); err != nil {
		t.Fatal(err)
	}
	if got := Limit(d, 1, MetricMonthlyRuns); got != 0 {
		t.Fatalf("delete got %d", got)
	}
	// 租户隔离
	_ = Set(d, 2, MetricMonthlyRuns, 7)
	if got := Limit(d, 1, MetricMonthlyRuns); got != 0 {
		t.Fatalf("tenant leak: %d", got)
	}
}

func addRun(t *testing.T, d *gorm.DB, tenantID int64, status int16, started time.Time) {
	t.Helper()
	r := model.TestRun{ID: model.NextID(), TenantID: tenantID, PlanID: 1, Status: status, StartedAt: started}
	if err := d.Create(&r).Error; err != nil {
		t.Fatal(err)
	}
}

func TestUsageMonthlyAndConcurrent(t *testing.T) {
	d := openTestDB(t)
	now := time.Now()
	addRun(t, d, 1, int16(commonv1.RunStatus_RUN_STATUS_PASSED), now)
	addRun(t, d, 1, int16(commonv1.RunStatus_RUN_STATUS_RUNNING), now)
	addRun(t, d, 1, int16(commonv1.RunStatus_RUN_STATUS_RUNNING), now)
	// 上月的不计入 monthly；其他租户不计入
	addRun(t, d, 1, int16(commonv1.RunStatus_RUN_STATUS_PASSED), now.AddDate(0, -2, 0))
	addRun(t, d, 2, int16(commonv1.RunStatus_RUN_STATUS_PASSED), now)

	if got := Usage(d, 1, MetricMonthlyRuns); got != 3 {
		t.Fatalf("monthly got %d", got)
	}
	if got := Usage(d, 1, MetricConcurrentRuns); got != 2 {
		t.Fatalf("concurrent got %d", got)
	}
}

func TestUsageArtifactBytes(t *testing.T) {
	d := openTestDB(t)
	for _, size := range []int64{100, 250} {
		a := model.Artifact{ID: model.NextID(), TenantID: 1, RunID: 1, Size: size}
		if err := d.Create(&a).Error; err != nil {
			t.Fatal(err)
		}
	}
	if got := Usage(d, 1, MetricArtifactBytes); got != 350 {
		t.Fatalf("got %d", got)
	}
	if got := Usage(d, 1, MetricWorkerSlots); got != 0 { // 无 dispatcher 注入时恒 0
		t.Fatalf("worker slots should be 0, got %d", got)
	}
}

func TestCheck(t *testing.T) {
	d := openTestDB(t)
	now := time.Now()
	addRun(t, d, 1, int16(commonv1.RunStatus_RUN_STATUS_PASSED), now)
	addRun(t, d, 1, int16(commonv1.RunStatus_RUN_STATUS_PASSED), now)

	if err := Check(d, 1, MetricMonthlyRuns, 10); err != nil {
		t.Fatalf("unlimited should pass: %v", err)
	}
	_ = Set(d, 1, MetricMonthlyRuns, 4)
	if err := Check(d, 1, MetricMonthlyRuns, 2); err != nil { // 2+2=4 不超
		t.Fatalf("boundary should pass: %v", err)
	}
	err := Check(d, 1, MetricMonthlyRuns, 3) // 2+3=5 > 4
	if err == nil {
		t.Fatal("should reject")
	}
	if ae := apperr.From(err); ae.HTTP != 429 || ae.Code != apperr.CodeQuotaExceeded {
		t.Fatalf("want 429 QUOTA_EXCEEDED, got %+v", ae)
	}
	// 其他租户不受限
	if err := Check(d, 2, MetricMonthlyRuns, 100); err != nil {
		t.Fatalf("tenant isolation: %v", err)
	}
}

func TestList(t *testing.T) {
	d := openTestDB(t)
	_ = Set(d, 1, MetricAICalls, 100)
	views := List(d, 1, 3) // workerOnline 注入 3
	if len(views) != len(Metrics) {
		t.Fatalf("views=%d", len(views))
	}
	for _, v := range views {
		switch v.Metric {
		case MetricAICalls:
			if v.Limit != 100 {
				t.Fatalf("ai_calls limit %d", v.Limit)
			}
		case MetricWorkerSlots:
			if v.Used != 3 {
				t.Fatalf("worker online injected got %d", v.Used)
			}
		}
	}
}
