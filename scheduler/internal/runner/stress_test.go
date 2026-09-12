package runner

import (
	"context"
	"errors"
	"testing"
	"time"

	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	"github.com/testpilot/testpilot/internal/apperr"
	"github.com/testpilot/testpilot/internal/db"
	"github.com/testpilot/testpilot/internal/dispatch"
	"github.com/testpilot/testpilot/internal/model"
	"github.com/testpilot/testpilot/internal/quota"
	"gorm.io/gorm"
)

func openStressTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	d, err := db.Open(t.TempDir()+"/stress.db", "", db.Pool{})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// seedStressPlan 建 tenant 1 下的 project + 单接口压测计划（无 env，走无环境路径）。
func seedStressPlan(t *testing.T, d *gorm.DB) int64 {
	t.Helper()
	proj := model.Project{ID: model.NextID(), TenantID: 1, Name: "proj"}
	if err := d.Create(&proj).Error; err != nil {
		t.Fatal(err)
	}
	api := model.HttpApi{ID: model.NextID(), TenantID: 1, ProjectID: proj.ID,
		Method: 1, URI: "/bench"}
	if err := d.Create(&api).Error; err != nil {
		t.Fatal(err)
	}
	plan := model.StressTestPlan{ID: model.NextID(), TenantID: 1, ProjectID: proj.ID,
		TargetType: 1, TargetID: api.ID, WorkerCount: 1,
		LoadProfile: model.JSON(`{"duration":"1s","concurrency_per_worker":1}`)}
	if err := d.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	return plan.ID
}

// TestTriggerStressQuotaGate 回归：TriggerStress（REST runStressPlan 与 gRPC 共用）
// 此前完全没有配额检查。quota 包无压测专用 metric，借 monthly_runs 做超限闸门
// （Usage 按 test_runs 计量，StressRun 不计入用量——只做门槛不做计量）。
func TestTriggerStressQuotaGate(t *testing.T) {
	d := openStressTestDB(t)
	planID := seedStressPlan(t, d)
	r := New(d, dispatch.New(d))

	// 无限额（无 quota 行）：越过配额闸门，走到 worker 选择 → NO_WORKER
	_, err := r.TriggerStress(context.Background(), 1, planID, 0, "tester")
	if !errors.Is(err, dispatch.ErrNoWorker) {
		t.Fatalf("without quota limit want ErrNoWorker, got %v", err)
	}

	// 限额 1 + 本月已有 1 条 run：触发压测必须被 429 拦截
	if err := d.Create(&model.TenantQuota{ID: model.NextID(), TenantID: 1,
		Metric: quota.MetricMonthlyRuns, Limit: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := d.Create(&model.TestRun{ID: model.NextID(), TenantID: 1, PlanID: planID,
		Status: int16(commonv1.RunStatus_RUN_STATUS_PASSED), StartedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	_, err = r.TriggerStress(context.Background(), 1, planID, 0, "tester")
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != apperr.CodeQuotaExceeded {
		t.Fatalf("over monthly quota want QUOTA_EXCEEDED, got %v", err)
	}
}
