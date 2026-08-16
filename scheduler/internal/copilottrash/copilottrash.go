// Package copilottrash 负责 Copilot 回收站的自动清理。
//
// 软删除的会话保留默认 30 天；每天清理一次超过保留期的会话及其消息。
// 手动彻底删除由 httpserver 直接处理，这里只做周期任务。
package copilottrash

import (
	"time"

	"github.com/testpilot/testpilot/internal/logging"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

// DefaultRetentionDays 回收站默认保留天数。
const DefaultRetentionDays = 30

// Start 启动每日回收站清理。days<=0 时关闭自动清理（与 retention 的语义一致）。
func Start(db *gorm.DB, days int) {
	if days <= 0 {
		logging.L.Infow("copilot trash auto cleanup disabled", "retention_days", days)
		return
	}
	logging.L.Infow("copilot trash cleanup enabled", "retention_days", days, "interval", "24h")
	go func() {
		Cleanup(db, days)
		for range time.Tick(24 * time.Hour) {
			Cleanup(db, days)
		}
	}()
}

// Cleanup 彻底删除回收站中超过 retentionDays 天的会话及其消息。
// 返回本次清理的会话数量。
func Cleanup(db *gorm.DB, retentionDays int) int64 {
	if retentionDays <= 0 {
		return 0
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	var ids []int64
	if err := db.Model(&model.CopilotSession{}).
		Where("deleted_at IS NOT NULL AND deleted_at < ?", cutoff).
		Order("deleted_at asc").Limit(500).Pluck("id", &ids).Error; err != nil {
		logging.L.Warnw("copilot trash cleanup query failed", "err", err)
		return 0
	}
	if len(ids) == 0 {
		return 0
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("session_id IN ?", ids).Delete(&model.CopilotMessage{}).Error; err != nil {
			return err
		}
		return tx.Where("id IN ?", ids).Delete(&model.CopilotSession{}).Error
	})
	if err != nil {
		logging.L.Warnw("copilot trash cleanup failed", "ids", len(ids), "err", err)
		return 0
	}
	logging.L.Infow("copilot trash cleanup", "sessions", len(ids))
	return int64(len(ids))
}
