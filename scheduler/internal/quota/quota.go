// Package quota 租户配额：按 metric 计量用量并对照上限拦截。
//
// 上限存 tenant_quota 表（无记录或 limit=0 = 不限）；用量按事实表实时计算，
// 不维护计数器（避免对账漂移）。
package quota

import (
	"fmt"
	"strings"
	"time"

	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	"github.com/testpilot/testpilot/internal/apperr"
	"github.com/testpilot/testpilot/internal/metrics"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 配额 metric 名。
const (
	MetricConcurrentRuns = "concurrent_runs" // 同时运行中的功能测试 run 数
	MetricMonthlyRuns    = "monthly_runs"    // 本月功能测试 run 数
	MetricArtifactBytes  = "artifact_bytes"  // 产物累计字节
	MetricAICalls        = "ai_calls"        // 本月 Copilot 写/触发次数
	MetricWorkerSlots    = "worker_slots"    // 同时在线 Worker 数
)

// Metrics 全部受支持 metric（管理 API 列出）。
var Metrics = []string{MetricConcurrentRuns, MetricMonthlyRuns, MetricArtifactBytes, MetricAICalls, MetricWorkerSlots}

// Limit 读上限；无记录 → 0（不限）。
func Limit(db *gorm.DB, tenantID int64, metric string) int64 {
	var rows []model.TenantQuota
	db.Where("tenant_id = ? AND metric = ?", tenantID, metric).Limit(1).Find(&rows)
	if len(rows) == 0 {
		return 0
	}
	return rows[0].Limit
}

// Usage 计算当前用量。
func Usage(db *gorm.DB, tenantID int64, metric string) int64 {
	monthStart := time.Date(time.Now().Year(), time.Now().Month(), 1, 0, 0, 0, 0, time.Local)
	var n int64
	switch metric {
	case MetricConcurrentRuns:
		db.Model(&model.TestRun{}).Where("tenant_id = ? AND status = ?",
			tenantID, int16(commonv1.RunStatus_RUN_STATUS_RUNNING)).Count(&n)
	case MetricMonthlyRuns:
		db.Model(&model.TestRun{}).Where("tenant_id = ? AND started_at >= ?", tenantID, monthStart).Count(&n)
	case MetricArtifactBytes:
		row := db.Model(&model.Artifact{}).Where("tenant_id = ?", tenantID).
			Select("COALESCE(SUM(size),0)").Row()
		_ = row.Scan(&n)
	case MetricAICalls:
		db.Model(&model.AuditLog{}).Where("tenant_id = ? AND actor = 2 AND created_at >= ?",
			tenantID, monthStart).Count(&n)
	case MetricWorkerSlots:
		// 在线 Worker 存于 Dispatcher 内存，由调用方注入；这里返回 0
		return 0
	}
	return n
}

// Check 校验 tenant 在 metric 上再增加 delta 是否超限；超限返回 429 QUOTA_EXCEEDED。
func Check(db *gorm.DB, tenantID int64, metric string, delta int64) error {
	limit := Limit(db, tenantID, metric)
	if limit <= 0 {
		return nil
	}
	used := Usage(db, tenantID, metric)
	if used+delta > limit {
		metrics.QuotaRejections.WithLabelValues(metric).Inc()
		return apperr.TooMany(apperr.CodeQuotaExceeded,
			fmt.Sprintf("quota %s exceeded: %d+%d > %d", metric, used, delta, limit))
	}
	return nil
}

// CheckTx 事务内配额校验（并发安全）：调用方须在同一事务内完成资源创建。
//
//   - PostgreSQL：SELECT ... FOR UPDATE 锁该租户 quota 行，同一租户的配额
//     检查串行化（check-then-act 不再可被并发穿透）；
//   - SQLite：依赖 db.Open 配置的 IMMEDIATE 写事务（_txlock=immediate），
//     写事务天然串行，直接读取即可。
func CheckTx(tx *gorm.DB, tenantID int64, metric string, delta int64) error {
	q := tx.Where("tenant_id = ?", tenantID)
	if !strings.HasPrefix(tx.Dialector.Name(), "sqlite") {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var rows []model.TenantQuota
	if err := q.Find(&rows).Error; err != nil {
		return err
	}
	var limit int64
	for _, row := range rows {
		if row.Metric == metric {
			limit = row.Limit
		}
	}
	if limit <= 0 {
		return nil
	}
	used := Usage(tx, tenantID, metric)
	if used+delta > limit {
		metrics.QuotaRejections.WithLabelValues(metric).Inc()
		return apperr.TooMany(apperr.CodeQuotaExceeded,
			fmt.Sprintf("quota %s exceeded: %d+%d > %d", metric, used, delta, limit))
	}
	return nil
}

// View 管理视图：上限 + 当前用量。
type View struct {
	Metric string `json:"metric"`
	Limit  int64  `json:"limit"`
	Used   int64  `json:"used"`
}

// List 列出全部 metric 的上限与用量。workerOnline 由 Dispatcher 提供（内存态）。
func List(db *gorm.DB, tenantID int64, workerOnline int64) []View {
	out := make([]View, 0, len(Metrics))
	for _, m := range Metrics {
		used := Usage(db, tenantID, m)
		if m == MetricWorkerSlots {
			used = workerOnline
		}
		out = append(out, View{Metric: m, Limit: Limit(db, tenantID, m), Used: used})
	}
	return out
}

// Set upsert 上限（limit<=0 表示不限，删除记录）。
func Set(db *gorm.DB, tenantID int64, metric string, limit int64) error {
	if limit <= 0 {
		return db.Where("tenant_id = ? AND metric = ?", tenantID, metric).Delete(&model.TenantQuota{}).Error
	}
	var q model.TenantQuota
	err := db.Where("tenant_id = ? AND metric = ?", tenantID, metric).First(&q).Error
	if err != nil {
		return db.Create(&model.TenantQuota{ID: model.NextID(), TenantID: tenantID, Metric: metric, Limit: limit}).Error
	}
	return db.Model(&q).Update("limit", limit).Error
}
