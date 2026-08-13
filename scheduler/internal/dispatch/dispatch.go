package dispatch

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	workerv1 "github.com/testpilot/testpilot/gen/worker/v1"
	"github.com/testpilot/testpilot/internal/apperr"
	"github.com/testpilot/testpilot/internal/logging"
	"github.com/testpilot/testpilot/internal/metrics"
	"github.com/testpilot/testpilot/internal/model"
	"github.com/testpilot/testpilot/internal/notify"
	"github.com/testpilot/testpilot/internal/quota"
	"gorm.io/gorm"
)

// Worker 表示一个已连接的 Worker 会话。
type Worker struct {
	ID             string
	Name           string
	Capabilities   []int32
	TenantID       int64 // 0 = 共享
	MaxConcurrency int32
	Tags           []string
	SDKVersion     string
	load           atomic.Int32
	stress         atomic.Bool // 压测独占中（不接功能任务）
	ConnectedAt    time.Time

	Send chan *workerv1.SchedulerCommand
}

// Load 返回当前负载。
func (w *Worker) Load() int32 { return w.load.Load() }

// InStress 返回是否处于压测独占。
func (w *Worker) InStress() bool { return w.stress.Load() }

// stressState 跟踪一次压测的多 Worker 完成进度。
type stressState struct {
	remaining atomic.Int32
	failed    atomic.Bool
}

// Dispatcher 维护 Worker 池并做能力路由 + 负载均衡。
type Dispatcher struct {
	db         *gorm.DB
	mu         sync.RWMutex
	workers    map[string]*Worker
	stressRuns sync.Map // runID(int64) → *stressState
}

func New(db *gorm.DB) *Dispatcher {
	return &Dispatcher{db: db, workers: map[string]*Worker{}}
}

// Register 注册 Worker。worker_slots 配额超限（仅租户专属 Worker，共享 0 不计）拒绝注册。
func (d *Dispatcher) Register(w *Worker) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if w.TenantID != 0 {
		limit := quota.Limit(d.db, w.TenantID, quota.MetricWorkerSlots)
		if limit > 0 {
			var online int64
			for _, x := range d.workers {
				if x.TenantID == w.TenantID {
					online++
				}
			}
			if online+1 > limit {
				metrics.QuotaRejections.WithLabelValues(quota.MetricWorkerSlots).Inc()
				return apperr.TooMany(apperr.CodeQuotaExceeded,
					fmt.Sprintf("quota worker_slots exceeded: %d+1 > %d", online, limit))
			}
		}
	}
	w.ConnectedAt = time.Now()
	d.workers[w.ID] = w
	d.workerMetricsLocked()
	return nil
}

// workerMetricsLocked 以池当前真实状态刷新 gauge。调用者须持有 d.mu。
func (d *Dispatcher) workerMetricsLocked() {
	var load int32
	for _, w := range d.workers {
		load += w.Load()
	}
	metrics.WorkersOnline.Set(float64(len(d.workers)))
	metrics.WorkerLoadSum.Set(float64(load))
}

// OnlineForTenant 统计租户当前在线 Worker 数（配额视图用）。
func (d *Dispatcher) OnlineForTenant(tenantID int64) int64 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	var n int64
	for _, w := range d.workers {
		if w.TenantID == tenantID {
			n++
		}
	}
	return n
}

// Unregister 移除 Worker。
func (d *Dispatcher) Unregister(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.workers, id)
	d.workerMetricsLocked()
}

// SetLoad 更新 Worker 负载。
func (d *Dispatcher) SetLoad(id string, n int32) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if w, ok := d.workers[id]; ok {
		w.load.Store(n)
		d.workerMetricsLocked()
	}
}

// Workers 返回当前在线 Worker（供调试/管理）。
func (d *Dispatcher) Workers() []*Worker {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]*Worker, 0, len(d.workers))
	for _, w := range d.workers {
		out = append(out, w)
	}
	return out
}

// requiredCapability 任务类型 → 所需能力。
func requiredCapability(t commonv1.TaskType) commonv1.Capability {
	switch t {
	case commonv1.TaskType_TASK_TYPE_FUNCTIONAL_LOWCODE:
		return commonv1.Capability_CAPABILITY_LOWCODE
	case commonv1.TaskType_TASK_TYPE_PLAYWRIGHT:
		return commonv1.Capability_CAPABILITY_PLAYWRIGHT
	case commonv1.TaskType_TASK_TYPE_STRESS:
		return commonv1.Capability_CAPABILITY_STRESS
	default:
		return commonv1.Capability_CAPABILITY_FUNCTIONAL
	}
}

func hasCapability(w *Worker, cap commonv1.Capability) bool {
	for _, c := range w.Capabilities {
		if c == int32(cap) {
			return true
		}
	}
	return false
}

var ErrNoWorker = apperr.Unavailable(apperr.CodeNoWorker, "no suitable worker online")

// Dispatch 选一个合格且最闲的 Worker 下发任务。
func (d *Dispatcher) Dispatch(task *workerv1.TaskAssignment) error {
	need := requiredCapability(task.GetTaskType())
	d.mu.RLock()
	var best *Worker
	for _, w := range d.workers {
		// 独占 Worker 只跑本租户；共享 Worker 跨租户
		if w.TenantID != 0 && w.TenantID != task.GetTenantId() {
			continue
		}
		if !hasCapability(w, need) {
			continue
		}
		if w.stress.Load() { // 压测独占，不接功能任务
			continue
		}
		if w.MaxConcurrency > 0 && w.Load() >= w.MaxConcurrency {
			continue
		}
		if best == nil || w.Load() < best.Load() {
			best = w
		}
	}
	d.mu.RUnlock()
	if best == nil {
		metrics.DispatchTotal.WithLabelValues("no_worker").Inc()
		return ErrNoWorker
	}
	best.load.Add(1)
	defer best.load.Add(-1)
	select {
	case best.Send <- &workerv1.SchedulerCommand{
		Command: &workerv1.SchedulerCommand_Task{Task: task},
	}:
		metrics.DispatchTotal.WithLabelValues("ok").Inc()
		return nil
	case <-time.After(5 * time.Second):
		metrics.DispatchTotal.WithLabelValues("send_timeout").Inc()
		return errors.New("worker send timeout")
	}
}

// Cancel 向所有在线 Worker 广播取消（简化：按 task_id）。
func (d *Dispatcher) Cancel(taskID, reason string) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, w := range d.workers {
		select {
		case w.Send <- &workerv1.SchedulerCommand{
			Command: &workerv1.SchedulerCommand_Cancel{
				Cancel: &workerv1.CancelTask{TaskId: taskID, Reason: reason},
			},
		}:
		default:
		}
	}
}

// ---- 压测 ----

// StressWorkers 返回可压测（stress 能力、非独占、租户匹配）的在线 Worker。
func (d *Dispatcher) StressWorkers(tenantID int64) []*Worker {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]*Worker, 0, len(d.workers))
	for _, w := range d.workers {
		if w.TenantID != 0 && w.TenantID != tenantID {
			continue
		}
		if !hasCapability(w, commonv1.Capability_CAPABILITY_STRESS) || w.stress.Load() {
			continue
		}
		out = append(out, w)
	}
	return out
}

// RegisterStressRun 登记一次压测的任务数（多 Worker 全部回报后才收尾）。
func (d *Dispatcher) RegisterStressRun(runID int64, tasks int) {
	st := &stressState{}
	st.remaining.Store(int32(tasks))
	d.stressRuns.Store(runID, st)
}

// DispatchStress 向指定 Worker 下发压测子任务并标记独占。
func (d *Dispatcher) DispatchStress(w *Worker, task *workerv1.TaskAssignment) error {
	w.stress.Store(true)
	w.load.Add(1)
	defer w.load.Add(-1)
	select {
	case w.Send <- &workerv1.SchedulerCommand{
		Command: &workerv1.SchedulerCommand_Task{Task: task},
	}:
		return nil
	case <-time.After(5 * time.Second):
		w.stress.Store(false)
		return errors.New("worker send timeout")
	}
}

// HandleStressMetrics 落库压测时序点（dev 内嵌存储；生产换 VictoriaMetrics）。
func (d *Dispatcher) HandleStressMetrics(batch *workerv1.StressMetricBatch) error {
	runID := mustInt64(batch.GetRunId())
	if runID == 0 || len(batch.GetPoints()) == 0 {
		return nil
	}
	var run model.StressRun
	if err := d.db.Select("tenant_id").Where("id = ?", runID).First(&run).Error; err != nil {
		return nil // 未知 run（可能已清理）：丢弃不报错
	}
	rows := make([]model.StressMetricPoint, 0, len(batch.GetPoints()))
	for _, p := range batch.GetPoints() {
		rows = append(rows, model.StressMetricPoint{
			ID:           model.NextID(),
			TenantID:     run.TenantID,
			StressRunID:  runID,
			Ts:           p.GetTs().AsTime(),
			Rps:          p.GetRps(),
			LatencyP50Ms: p.GetLatencyP50Ms(),
			LatencyP95Ms: p.GetLatencyP95Ms(),
			LatencyP99Ms: p.GetLatencyP99Ms(),
			ErrorRate:    p.GetErrorRate(),
			Concurrency:  int(p.GetConcurrency()),
		})
	}
	return d.db.Create(&rows).Error
}

// handleStressResult 压测子任务回报：全部到齐后汇总收尾。
func (d *Dispatcher) handleStressResult(res *workerv1.TaskResult) error {
	runID := mustInt64(res.GetRunId())
	v, ok := d.stressRuns.Load(runID)
	if !ok {
		return nil
	}
	st := v.(*stressState)
	if res.GetStatus() != commonv1.RunStatus_RUN_STATUS_PASSED {
		st.failed.Store(true)
	}
	if st.remaining.Add(-1) > 0 {
		return nil // 还有其他 Worker 未回报
	}
	d.stressRuns.Delete(runID)

	// 聚合时序点为摘要
	var agg struct {
		Samples  int64
		AvgRps   float64
		PeakRps  float64
		MaxP95   float64
		AvgErr   float64
		MaxUsers int64
	}
	d.db.Model(&model.StressMetricPoint{}).Where("stress_run_id = ?", runID).
		Select("count(*) as samples, coalesce(avg(rps),0) as avg_rps, coalesce(max(rps),0) as peak_rps, " +
			"coalesce(max(latency_p95_ms),0) as max_p95, coalesce(avg(error_rate),0) as avg_err, " +
			"coalesce(max(concurrency),0) as max_users").
		Scan(&agg)

	status := commonv1.RunStatus_RUN_STATUS_PASSED
	if st.failed.Load() {
		status = commonv1.RunStatus_RUN_STATUS_FAILED
	}
	now := time.Now()
	summary := fmt.Sprintf(`{"samples":%d,"avg_rps":%.1f,"peak_rps":%.1f,"max_p95_ms":%.1f,"avg_error_rate":%.4f,"max_concurrency":%d}`,
		agg.Samples, agg.AvgRps, agg.PeakRps, agg.MaxP95, agg.AvgErr, agg.MaxUsers)
	err := d.db.Model(&model.StressRun{}).Where("id = ?", runID).
		Updates(map[string]any{
			"status":      int16(status),
			"finished_at": &now,
			"summary":     summary,
		}).Error
	if err == nil {
		metrics.StressRunsTotal.WithLabelValues(metrics.RunStatusName(int16(status))).Inc()
		go notify.StressFinished(d.db, runID)
	}
	return err
}

// HandleTaskResult 落库任务结果并推进 run 状态。w 为上报的 Worker（压测任务用于解除独占）。
func (d *Dispatcher) HandleTaskResult(w *Worker, res *workerv1.TaskResult) error {
	// 压测任务结果走独立收尾路径
	var srun model.StressRun
	if err := d.db.Select("id", "tenant_id").Where("id = ?", mustInt64(res.GetRunId())).First(&srun).Error; err == nil {
		w.stress.Store(false)
		return d.handleStressResult(res)
	}
	return d.db.Transaction(func(tx *gorm.DB) error {
		for _, cr := range res.GetCaseResults() {
			tx.Model(&model.TestCaseResult{}).
				Where("id = ?", mustInt64(cr.GetId())).
				Updates(map[string]any{
					"status":      int16(cr.GetStatus()),
					"duration_ms": int(cr.GetDuration().AsDuration().Milliseconds()),
					"error":       cr.GetError(),
				})
		}
		stepTenant := map[int64]int64{}
		for _, sr := range res.GetStepResults() {
			crID := mustInt64(sr.GetCaseResultId())
			tid, ok := stepTenant[crID]
			if !ok {
				var cr model.TestCaseResult
				if err := tx.Select("tenant_id").Where("id = ?", crID).First(&cr).Error; err == nil {
					tid = cr.TenantID
				}
				stepTenant[crID] = tid
			}
			row := &model.TestStepResult{
				ID:           model.NextID(),
				TenantID:     tid,
				CaseResultID: crID,
				StepPath:     sr.GetStepPath(),
				Status:       int16(sr.GetStatus()),
				DurationMs:   int(sr.GetDuration().AsDuration().Milliseconds()),
				Request:      structToJSON(sr.GetRequest()),
				Response:     structToJSON(sr.GetResponse()),
				Assertions:   assertionsToJSON(sr.GetAssertions()),
				Logs:         stringsToJSON(sr.GetLogs()),
			}
			if err := tx.Create(row).Error; err != nil {
				return err
			}
			for _, a := range sr.GetArtifacts() {
				// 产物字节配额：超限丢弃该产物（步骤结果仍保留），记日志
				if err := quota.Check(tx, tid, quota.MetricArtifactBytes, a.GetSize()); err != nil {
					metrics.ArtifactsDropped.Inc()
					logging.L.Warnw("artifact dropped by quota", "tenant", tid, "kind", a.GetKind(), "size", a.GetSize())
					continue
				}
				art := &model.Artifact{
					ID:           model.NextID(),
					TenantID:     tid,
					RunID:        mustInt64(res.GetRunId()),
					StepResultID: row.ID,
					Kind:         model.ArtifactKindFromString(a.GetKind()),
					URI:          a.GetUri(),
					Size:         a.GetSize(),
				}
				if err := tx.Create(art).Error; err != nil {
					return err
				}
			}
		}
		return d.maybeFinishRun(tx, mustInt64(res.GetRunId()))
	})
}

// maybeFinishRun 当 run 下所有 case 均结束时汇总并收尾。
func (d *Dispatcher) maybeFinishRun(tx *gorm.DB, runID int64) error {
	var total, done, failed int64
	tx.Model(&model.TestCaseResult{}).Where("run_id = ?", runID).Count(&total)
	tx.Model(&model.TestCaseResult{}).
		Where("run_id = ? AND status IN ?", runID, []int16{
			int16(commonv1.CaseStatus_CASE_STATUS_PASSED),
			int16(commonv1.CaseStatus_CASE_STATUS_FAILED),
			int16(commonv1.CaseStatus_CASE_STATUS_SKIPPED),
		}).Count(&done)
	tx.Model(&model.TestCaseResult{}).
		Where("run_id = ? AND status = ?", runID, int16(commonv1.CaseStatus_CASE_STATUS_FAILED)).
		Count(&failed)
	if total == 0 || done < total {
		return nil
	}
	status := commonv1.RunStatus_RUN_STATUS_PASSED
	if failed > 0 {
		status = commonv1.RunStatus_RUN_STATUS_FAILED
	}
	var runRow model.TestRun
	tx.Select("trigger", "started_at").Where("id = ?", runID).First(&runRow)
	now := time.Now()
	summary := fmt.Sprintf(`{"total":%d,"passed":%d,"failed":%d,"skipped":0}`, total, total-failed, failed)
	err := tx.Model(&model.TestRun{}).Where("id = ?", runID).
		Updates(map[string]any{
			"status":      int16(status),
			"finished_at": &now,
			"summary":     summary,
		}).Error
	if err == nil {
		st := metrics.RunStatusName(int16(status))
		metrics.RunsTotal.WithLabelValues(st, metrics.TriggerName(runRow.Trigger)).Inc()
		metrics.RunDuration.WithLabelValues(st).Observe(time.Since(runRow.StartedAt).Seconds())
		go notify.RunFinished(d.db, runID)
	}
	return err
}

func mustInt64(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case string:
		var x int64
		fmt.Sscan(n, &x)
		return x
	}
	return 0
}
