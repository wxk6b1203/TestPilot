// Package cronsched 定时调度：robfig/cron 驱动 TestPlan 周期运行。
//
// - 标准 5 段 cron 表达式（分 时 日 月 周）
// - overlap_policy: 1=上次运行未结束则跳过（默认） 2=允许并发
// - misfire：进程重启后若 NextRunAt 已过期超过 2 分钟，补跑一次
package cronsched

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	"github.com/testpilot/testpilot/internal/logging"
	"github.com/testpilot/testpilot/internal/model"
	"github.com/testpilot/testpilot/internal/runner"
	"gorm.io/gorm"
)

const misfireGrace = 2 * time.Minute

type Scheduler struct {
	db      *gorm.DB
	run     *runner.Runner
	cr      *cron.Cron
	mu      sync.Mutex
	entries map[int64]cron.EntryID
}

func New(db *gorm.DB, run *runner.Runner) *Scheduler {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	return &Scheduler{
		db:      db,
		run:     run,
		cr:      cron.New(cron.WithParser(parser)),
		entries: map[int64]cron.EntryID{},
	}
}

// Start 加载全部启用的调度并启动；对 misfire 的调度补跑一次。
func (s *Scheduler) Start() {
	var rows []model.Schedule
	s.db.Where("enabled = ?", true).Find(&rows)
	now := time.Now()
	for _, sc := range rows {
		s.add(&sc)
		if sc.NextRunAt != nil && sc.NextRunAt.Before(now.Add(-misfireGrace)) {
			logging.L.Infow("schedule misfire catch-up", "id", sc.ID, "next", sc.NextRunAt)
			s.fire(sc.ID)
		}
	}
	s.cr.Start()
	logging.L.Infow("cron scheduler started", "entries", len(rows))
}

func (s *Scheduler) Stop() { s.cr.Stop() }

// Sync 创建/更新后同步 cron 条目（禁用或表达式非法时移除）。
func (s *Scheduler) Sync(sc *model.Schedule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.entries[sc.ID]; ok {
		s.cr.Remove(old)
		delete(s.entries, sc.ID)
	}
	if sc.Enabled {
		s.addLocked(sc)
	}
}

// Remove 删除条目。
func (s *Scheduler) Remove(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.entries[id]; ok {
		s.cr.Remove(old)
		delete(s.entries, id)
	}
}

func (s *Scheduler) add(sc *model.Schedule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addLocked(sc)
}

func (s *Scheduler) addLocked(sc *model.Schedule) {
	id := sc.ID
	eid, err := s.cr.AddFunc(sc.CronExpr, func() { s.fire(id) })
	if err != nil {
		logging.L.Warnw("schedule cron expr invalid", "id", sc.ID, "expr", sc.CronExpr, "err", err)
		return
	}
	s.entries[sc.ID] = eid
	// 记录下次触发时间（misfire 检测用）
	if next := s.nextOf(sc.CronExpr); next != nil {
		s.db.Model(&model.Schedule{}).Where("id = ?", sc.ID).Update("next_run_at", *next)
	}
}

func (s *Scheduler) nextOf(expr string) *time.Time {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	sd, err := parser.Parse(expr)
	if err != nil {
		return nil
	}
	next := sd.Next(time.Now())
	return &next
}

// fire 触发一次运行：overlap 策略 + 状态回写。
func (s *Scheduler) fire(scheduleID int64) {
	var sc model.Schedule
	if err := s.db.First(&sc, scheduleID).Error; err != nil || !sc.Enabled {
		return
	}
	if sc.OverlapPolicy != 2 {
		var running int64
		s.db.Model(&model.TestRun{}).Where("plan_id = ? AND status = ?",
			sc.PlanID, int16(commonv1.RunStatus_RUN_STATUS_RUNNING)).Count(&running)
		if running > 0 {
			logging.L.Infow("schedule skipped (overlap)", "id", sc.ID, "plan", sc.PlanID)
			s.touch(&sc)
			return
		}
	}
	runID, err := s.run.Trigger(context.Background(), sc.TenantID, sc.PlanID, sc.EnvID,
		int16(commonv1.TriggerType_TRIGGER_TYPE_SCHEDULED), "schedule:"+strconv.FormatInt(sc.ID, 10))
	if err != nil {
		logging.L.Warnw("schedule fire failed", "id", sc.ID, "err", err)
	} else {
		logging.L.Infow("schedule fired", "id", sc.ID, "run", runID)
	}
	s.touch(&sc)
}

func (s *Scheduler) touch(sc *model.Schedule) {
	now := time.Now()
	updates := map[string]any{"last_run_at": now}
	if next := s.nextOf(sc.CronExpr); next != nil {
		updates["next_run_at"] = *next
	}
	s.db.Model(&model.Schedule{}).Where("id = ?", sc.ID).Updates(updates)
}
