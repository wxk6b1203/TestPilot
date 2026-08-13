package retention

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/testpilot/testpilot/internal/db"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

// TestCleanup 级联删除 30 天前的 run/stress run（含产物文件），保留近期数据。
func TestCleanup(t *testing.T) {
	tmp := t.TempDir()
	d, err := db.Open(filepath.Join(tmp, "test.db"), "", db.Pool{})
	if err != nil {
		t.Fatal(err)
	}
	artDir := filepath.Join(tmp, "artifacts")
	if err := os.MkdirAll(filepath.Join(artDir, "old"), 0o755); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	old := now.AddDate(0, 0, -40)

	// 旧 run（应删）+ 近期 run（应留），各带 case/step/artifact
	mkRun := func(id int64, started time.Time) {
		d.Create(&model.TestRun{ID: id, TenantID: 1, PlanID: 1, Status: 2, StartedAt: started})
		d.Create(&model.TestCaseResult{ID: id * 10, TenantID: 1, RunID: id, CaseID: 1, Status: 2})
		d.Create(&model.TestStepResult{ID: id * 100, TenantID: 1, CaseResultID: id * 10, Status: 2})
	}
	mkRun(1001, old)
	mkRun(1002, now)

	// 旧产物：URI 指向真实文件
	oldFile := filepath.Join(artDir, "old", "shot.png")
	if err := os.WriteFile(oldFile, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	d.Create(&model.Artifact{ID: 9001, TenantID: 1, RunID: 1001, StepResultID: 100100,
		Kind: model.ArtifactKindScreenshot, URI: "old/shot.png", Size: 3})
	d.Create(&model.Artifact{ID: 9002, TenantID: 1, RunID: 1002, StepResultID: 100200,
		Kind: model.ArtifactKindScreenshot, URI: "old/keep.png", Size: 3})

	// 压测：旧（应删）+ 近期（应留）
	d.Create(&model.StressRun{ID: 2001, TenantID: 1, StressPlanID: 1, Status: 2, StartedAt: old})
	d.Create(&model.StressMetricPoint{ID: 3001, TenantID: 1, StressRunID: 2001, Ts: old})
	d.Create(&model.StressRun{ID: 2002, TenantID: 1, StressPlanID: 1, Status: 2, StartedAt: now})
	d.Create(&model.StressMetricPoint{ID: 3002, TenantID: 1, StressRunID: 2002, Ts: now})

	cleanup(d, artDir, 30)

	count := func(m any, where string, args ...any) int64 {
		var n int64
		d.Model(m).Where(where, args...).Count(&n)
		return n
	}
	for name, n := range map[string]int64{
		"old run":        count(&model.TestRun{}, "id = 1001"),
		"old case":       count(&model.TestCaseResult{}, "run_id = 1001"),
		"old step":       count(&model.TestStepResult{}, "case_result_id = 10010"),
		"old artifact":   count(&model.Artifact{}, "run_id = 1001"),
		"old stress run": count(&model.StressRun{}, "id = 2001"),
		"old metric":     count(&model.StressMetricPoint{}, "stress_run_id = 2001"),
	} {
		if n != 0 {
			t.Errorf("%s: 应被删除，剩 %d 行", name, n)
		}
	}
	for name, n := range map[string]int64{
		"recent run":        count(&model.TestRun{}, "id = 1002"),
		"recent case":       count(&model.TestCaseResult{}, "run_id = 1002"),
		"recent step":       count(&model.TestStepResult{}, "case_result_id = 10020"),
		"recent artifact":   count(&model.Artifact{}, "run_id = 1002"),
		"recent stress run": count(&model.StressRun{}, "id = 2002"),
		"recent metric":     count(&model.StressMetricPoint{}, "stress_run_id = 2002"),
	} {
		if n != 1 {
			t.Errorf("%s: 应保留 1 行，实际 %d", name, n)
		}
	}
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Error("旧产物文件应被删除")
	}
}

// TestStartDisabled days<=0 时不启动清理（零副作用保障）。
func TestStartDisabled(t *testing.T) {
	var d *gorm.DB // nil db：若 Start 误启动 goroutine 会在首次 cleanup 时 panic
	Start(d, t.TempDir(), 0, 60)
}
