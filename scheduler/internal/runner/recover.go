package runner

import (
	"sync"
	"time"

	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	"github.com/testpilot/testpilot/internal/dispatch"
	"github.com/testpilot/testpilot/internal/logging"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

// RecoverInterruptedRuns 启动时终结进程重启遗留的 RUNNING run/case/stress run。
// 没有它，历史 RUNNING 会永久卡死（maybeFinishRun 计数永不齐），
// 并连带 cron overlap 策略（按 RUNNING 计数）永久跳过定时任务。
func RecoverInterruptedRuns(db *gorm.DB) {
	now := time.Now()
	summary := `{"error":"interrupted by scheduler restart"}`
	runRes := db.Model(&model.TestRun{}).
		Where("status = ?", int16(commonv1.RunStatus_RUN_STATUS_RUNNING)).
		Updates(map[string]any{
			"status":      int16(commonv1.RunStatus_RUN_STATUS_FAILED),
			"finished_at": &now,
			"summary":     summary,
		})
	if runRes.Error != nil {
		logging.L.Warnw("recover: fail interrupted runs failed", "err", runRes.Error)
	} else if runRes.RowsAffected > 0 {
		logging.L.Infow("recover: interrupted runs marked failed", "runs", runRes.RowsAffected)
	}
	caseRes := db.Model(&model.TestCaseResult{}).
		Where("status = ?", int16(commonv1.CaseStatus_CASE_STATUS_RUNNING)).
		Updates(map[string]any{
			"status": int16(commonv1.CaseStatus_CASE_STATUS_FAILED),
			"error":  "interrupted by scheduler restart",
		})
	if caseRes.Error != nil {
		logging.L.Warnw("recover: fail interrupted cases failed", "err", caseRes.Error)
	}
	stressRes := db.Model(&model.StressRun{}).
		Where("status = ?", int16(commonv1.RunStatus_RUN_STATUS_RUNNING)).
		Updates(map[string]any{
			"status":      int16(commonv1.RunStatus_RUN_STATUS_FAILED),
			"finished_at": &now,
			"summary":     summary,
		})
	if stressRes.Error != nil {
		logging.L.Warnw("recover: fail interrupted stress runs failed", "err", stressRes.Error)
	}
}

// ReapStaleRuns 周期性终结超时未收尾的运行（worker 失联/任务丢失兜底）。
// maxAge 内的 RUNNING run 视为健康；超过则强制 FAILED 并清理收尾跟踪。
func ReapStaleRuns(db *gorm.DB, disp *dispatch.Dispatcher, maxAge time.Duration) {
	cutoff := time.Now().Add(-maxAge)
	now := time.Now()
	reapSummary := `{"error":"run timed out (reaper)"}`

	var runIDs []int64
	db.Model(&model.TestRun{}).
		Where("status = ? AND started_at < ?", int16(commonv1.RunStatus_RUN_STATUS_RUNNING), cutoff).
		Limit(200).Pluck("id", &runIDs)
	for _, id := range runIDs {
		db.Model(&model.TestCaseResult{}).
			Where("run_id = ? AND status = ?", id, int16(commonv1.CaseStatus_CASE_STATUS_RUNNING)).
			Updates(map[string]any{
				"status": int16(commonv1.CaseStatus_CASE_STATUS_FAILED),
				"error":  "run reaped: worker unreachable",
			})
		res := db.Model(&model.TestRun{}).
			Where("id = ? AND status = ?", id, int16(commonv1.RunStatus_RUN_STATUS_RUNNING)).
			Updates(map[string]any{
				"status":      int16(commonv1.RunStatus_RUN_STATUS_FAILED),
				"finished_at": &now,
				"summary":     reapSummary,
			})
		if res.Error == nil && res.RowsAffected > 0 {
			logging.L.Warnw("reaper: stale run reaped", "run_id", id)
		}
	}

	var sids []int64
	db.Model(&model.StressRun{}).
		Where("status = ? AND started_at < ?", int16(commonv1.RunStatus_RUN_STATUS_RUNNING), cutoff).
		Limit(200).Pluck("id", &sids)
	for _, id := range sids {
		res := db.Model(&model.StressRun{}).
			Where("id = ? AND status = ?", id, int16(commonv1.RunStatus_RUN_STATUS_RUNNING)).
			Updates(map[string]any{
				"status":      int16(commonv1.RunStatus_RUN_STATUS_FAILED),
				"finished_at": &now,
				"summary":     reapSummary,
			})
		if res.Error == nil && res.RowsAffected > 0 {
			logging.L.Warnw("reaper: stale stress run reaped", "run_id", id)
		}
		if disp != nil {
			disp.DropStressRun(id) // 迟到结果不再走收尾（stressRuns 条目已删，直接忽略）
		}
	}
}

// StaleRunAge 超时 reaper 的运行年龄阈值（正常功能 run 受 plan timeout 约束，
// 压测为 duration+120s 宽限；2h 足以覆盖所有正常形态，超时即视为失联兜底）。
const StaleRunAge = 2 * time.Hour

// Reaper 周期与心跳失联阈值（心跳间隔 10s，3 个周期未上报即失联）。
const (
	ReapInterval     = 30 * time.Second
	WorkerStaleAfter = 90 * time.Second
	ReapRunsInterval = 5 * time.Minute
)

// StartReapers 启动后台回收循环（失联 Worker 剔除 + 超时 run 终结）。返回 stop 函数。
func StartReapers(db *gorm.DB, disp *dispatch.Dispatcher) func() {
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		t := time.NewTicker(ReapInterval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				if stale := disp.ReapStaleWorkers(WorkerStaleAfter); len(stale) > 0 {
					logging.L.Warnw("reaper: stale workers evicted", "ids", stale)
				}
			case <-stop:
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		t := time.NewTicker(ReapRunsInterval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				ReapStaleRuns(db, disp, StaleRunAge)
			case <-stop:
				return
			}
		}
	}()
	return func() {
		close(stop)
		wg.Wait()
	}
}
