// Package retention 数据保留：周期清理过期的运行结果与产物文件。
//
// TP_RETENTION_RUN_DAYS（默认 0 = 永久保留）。以 TestRun.started_at 为基准级联删除：
// StepResult/CaseResult/Artifact（含磁盘文件）；压测侧按 StressRun.started_at +
// StressMetricPoint.ts 清理。每轮最多 500 个 run，避免长事务。
package retention

import (
	"os"
	"path/filepath"
	"time"

	"github.com/testpilot/testpilot/internal/logging"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

// Start 启动后台清理（days<=0 不启动）；立即先跑一轮，之后每小时一轮。
func Start(db *gorm.DB, artifactDir string, days int) {
	if days <= 0 {
		return
	}
	logging.L.Infow("retention enabled", "run_days", days)
	go func() {
		cleanup(db, artifactDir, days)
		for range time.Tick(time.Hour) {
			cleanup(db, artifactDir, days)
		}
	}()
}

func cleanup(db *gorm.DB, artifactDir string, days int) {
	cutoff := time.Now().AddDate(0, 0, -days)
	var artCount int

	// 功能测试：按 run 级联
	var runIDs []int64
	db.Model(&model.TestRun{}).Where("started_at < ?", cutoff).Limit(500).Pluck("id", &runIDs)
	if len(runIDs) > 0 {
		// 产物文件先删磁盘（防路径穿越，与 artifact_handlers 同一规则）
		var arts []model.Artifact
		db.Where("run_id IN ?", runIDs).Find(&arts)
		artCount = len(arts)
		for _, a := range arts {
			p := filepath.Join(artifactDir, filepath.Clean("/"+a.URI))
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				logging.L.Warnw("retention: remove artifact file failed", "path", p, "err", err)
			}
		}
		var caseIDs []int64
		db.Model(&model.TestCaseResult{}).Where("run_id IN ?", runIDs).Pluck("id", &caseIDs)
		if len(caseIDs) > 0 {
			db.Where("case_result_id IN ?", caseIDs).Delete(&model.TestStepResult{})
		}
		db.Where("run_id IN ?", runIDs).Delete(&model.Artifact{})
		db.Where("run_id IN ?", runIDs).Delete(&model.TestCaseResult{})
		db.Where("id IN ?", runIDs).Delete(&model.TestRun{})
	}

	// 压测：时序点 + run
	var srunIDs []int64
	db.Model(&model.StressRun{}).Where("started_at < ?", cutoff).Limit(500).Pluck("id", &srunIDs)
	if len(srunIDs) > 0 {
		db.Where("stress_run_id IN ?", srunIDs).Delete(&model.StressMetricPoint{})
		db.Where("id IN ?", srunIDs).Delete(&model.StressRun{})
	}

	if len(runIDs)+len(srunIDs) > 0 {
		logging.L.Infow("retention cleanup", "runs", len(runIDs), "stress_runs", len(srunIDs),
			"artifact_files", artCount)
	}
}
