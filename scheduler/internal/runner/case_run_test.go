package runner

import (
	"context"
	"testing"

	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	"github.com/testpilot/testpilot/internal/dispatch"
	"github.com/testpilot/testpilot/internal/model"
)

func TestRunCaseDispatchesSingleCase(t *testing.T) {
	d := openTestDB(t)
	fx := seedPlanData(t, d)
	// seedPlanData 的 case 是声明式；直接把 env 绑到该项目，单用例运行应创建 run + 1 个 case result。
	disp := dispatch.New(d)
	disp.Register(mkWorker("w1", 0, commonv1.Capability_CAPABILITY_FUNCTIONAL))
	r := New(d, disp)

	runID, err := r.RunCase(context.Background(), 1, fx.caseID, fx.envID, "tester")
	if err != nil {
		t.Fatalf("RunCase: %v", err)
	}
	if runID <= 0 {
		t.Fatalf("want positive run id, got %d", runID)
	}
	var crs []model.TestCaseResult
	if err := d.Where("run_id = ?", runID).Find(&crs).Error; err != nil {
		t.Fatal(err)
	}
	if len(crs) != 1 || crs[0].CaseID != fx.caseID {
		t.Fatalf("want 1 case result for case %d, got %+v", fx.caseID, crs)
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
}

func TestRunCaseMissingCase(t *testing.T) {
	d := openTestDB(t)
	r := New(d, dispatch.New(d))
	if _, err := r.RunCase(context.Background(), 1, model.NextID(), 0, "tester"); err == nil {
		t.Fatal("missing case should error")
	}
}
