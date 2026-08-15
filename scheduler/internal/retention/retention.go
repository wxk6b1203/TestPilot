// Package retention 数据保留：周期清理过期的运行结果与产物文件。
//
// TP_RETENTION_RUN_DAYS（默认 0 = 永久保留）。以 TestRun.started_at 为基准级联删除：
// StepResult/CaseResult/Artifact（含磁盘文件）；压测侧按 StressRun.started_at +
// StressMetricPoint.ts 清理。每轮最多 500 个 run，避免长事务。
package retention

import (
	"time"

	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	"github.com/testpilot/testpilot/internal/artifactstore"
	"github.com/testpilot/testpilot/internal/logging"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

// Start 启动后台清理（days<=0 不启动）；立即先跑一轮，之后每 intervalMin 分钟一轮。
// 产物文件经 store 删除（local 删磁盘 / s3 删对象）。
func Start(db *gorm.DB, store artifactstore.Backend, days, intervalMin int) {
	if days <= 0 {
		return
	}
	if intervalMin <= 0 {
		intervalMin = 60
	}
	logging.L.Infow("retention enabled", "run_days", days, "interval_min", intervalMin)
	go func() {
		cleanup(db, store, days)
		for range time.Tick(time.Duration(intervalMin) * time.Minute) {
			cleanup(db, store, days)
		}
	}()
}

func cleanup(db *gorm.DB, store artifactstore.Backend, days int) {
	cutoff := time.Now().AddDate(0, 0, -days)
	var artCount int

	// 功能测试：按 run 级联（只清理终态——运行中的 run 会被并发删除导致
	// worker 回报 UPDATE 落空、孤儿结果残留 + concurrent_runs 配额永久占用）
	var runIDs []int64
	db.Model(&model.TestRun{}).
		Where("started_at < ? AND status IN ?", cutoff, []int16{
			int16(commonv1.RunStatus_RUN_STATUS_PASSED),
			int16(commonv1.RunStatus_RUN_STATUS_FAILED),
			int16(commonv1.RunStatus_RUN_STATUS_ABORTED),
			int16(commonv1.RunStatus_RUN_STATUS_TIMEOUT),
		}).
		Order("started_at asc").Limit(500).Pluck("id", &runIDs)
	if len(runIDs) > 0 {
		// 产物文件先删（路径穿越防护由后端负责）
		var arts []model.Artifact
		db.Where("run_id IN ?", runIDs).Find(&arts)
		artCount = len(arts)
		for _, a := range arts {
			if err := store.Delete(a.TenantID, a.URI); err != nil {
				logging.L.Warnw("retention: remove artifact failed", "uri", a.URI, "err", err)
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

	// 压测：时序点 + run（同样只清终态）
	var srunIDs []int64
	db.Model(&model.StressRun{}).
		Where("started_at < ? AND status IN ?", cutoff, []int16{
			int16(commonv1.RunStatus_RUN_STATUS_PASSED),
			int16(commonv1.RunStatus_RUN_STATUS_FAILED),
			int16(commonv1.RunStatus_RUN_STATUS_ABORTED),
			int16(commonv1.RunStatus_RUN_STATUS_TIMEOUT),
		}).
		Order("started_at asc").Limit(500).Pluck("id", &srunIDs)
	if len(srunIDs) > 0 {
		db.Where("stress_run_id IN ?", srunIDs).Delete(&model.StressMetricPoint{})
		db.Where("id IN ?", srunIDs).Delete(&model.StressRun{})
	}

	if len(runIDs)+len(srunIDs) > 0 {
		logging.L.Infow("retention cleanup", "runs", len(runIDs), "stress_runs", len(srunIDs),
			"artifact_files", artCount)
	}
}
