package httpserver

import (
	"github.com/gofiber/fiber/v3"
	"strconv"

	"github.com/testpilot/testpilot/internal/apperr"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

// ---- 定时调度 CRUD（member）----

func (s *Server) listSchedules(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	return listOf[model.Schedule](s.db, ctx, func(q *gorm.DB) *gorm.DB {
		q = q.Where("tenant_id = ?", c.TenantID)
		if pid := queryInt(ctx, "plan_id"); pid != 0 {
			q = q.Where("plan_id = ?", pid)
		}
		return q.Order("id desc")
	})
}

type scheduleReq struct {
	PlanID        string `json:"plan_id"`
	EnvID         string `json:"env_id"`
	Name          string `json:"name"`
	CronExpr      string `json:"cron_expr"`
	OverlapPolicy int16  `json:"overlap_policy"`
	Enabled       *bool  `json:"enabled"`
}

func (s *Server) createSchedule(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	var in scheduleReq
	if !decode(ctx, &in) {
		return nil
	}
	planID, perr := strconv.ParseInt(in.PlanID, 10, 64)
	if perr != nil || in.CronExpr == "" {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "plan_id 与 cron_expr 必填"))
	}
	if !ensureEntity(s.db, ctx, "plan", planID) {
		return nil
	}
	envID, _ := strconv.ParseInt(in.EnvID, 10, 64)
	if envID != 0 && !ensureEntity(s.db, ctx, "environment", envID) {
		return nil
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	if in.OverlapPolicy != 1 && in.OverlapPolicy != 2 {
		in.OverlapPolicy = 1
	}
	row := &model.Schedule{
		ID: model.NextID(), TenantID: c.TenantID, PlanID: planID, EnvID: envID,
		Name: in.Name, CronExpr: in.CronExpr, OverlapPolicy: in.OverlapPolicy, Enabled: enabled,
	}
	if err := s.db.Create(row).Error; err != nil {
		return writeAppErr(ctx, apperr.Internal(err.Error()))
	}
	s.cron.Sync(row)
	return writeJSON(ctx, fiber.StatusOK, row)
}

func (s *Server) updateSchedule(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	id, ok := pathID(ctx, "id")
	if !ok {
		return nil
	}
	var row model.Schedule
	if err := s.db.Where("id = ? AND tenant_id = ?", id, c.TenantID).First(&row).Error; err != nil {
		return writeAppErr(ctx, apperr.NotFound(apperr.CodeNotFound, "schedule not found"))
	}
	var in scheduleReq
	if !decode(ctx, &in) {
		return nil
	}
	if in.PlanID != "" {
		if pid, err := strconv.ParseInt(in.PlanID, 10, 64); err == nil {
			if !ensureEntity(s.db, ctx, "plan", pid) {
				return nil
			}
			row.PlanID = pid
		}
	}
	if in.EnvID != "" {
		if eid, err := strconv.ParseInt(in.EnvID, 10, 64); err == nil {
			if eid != 0 && !ensureEntity(s.db, ctx, "environment", eid) {
				return nil
			}
			row.EnvID = eid
		}
	}
	if in.Name != "" {
		row.Name = in.Name
	}
	if in.CronExpr != "" {
		row.CronExpr = in.CronExpr
	}
	if in.OverlapPolicy == 1 || in.OverlapPolicy == 2 {
		row.OverlapPolicy = in.OverlapPolicy
	}
	if in.Enabled != nil {
		row.Enabled = *in.Enabled
	}
	if err := s.db.Save(&row).Error; err != nil {
		return writeAppErr(ctx, apperr.Internal(err.Error()))
	}
	s.cron.Sync(&row)
	return writeJSON(ctx, fiber.StatusOK, row)
}

func (s *Server) deleteSchedule(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	id, ok := pathID(ctx, "id")
	if !ok {
		return nil
	}
	res := s.db.Where("id = ? AND tenant_id = ?", id, c.TenantID).Delete(&model.Schedule{})
	if res.RowsAffected == 0 {
		return writeAppErr(ctx, apperr.NotFound(apperr.CodeNotFound, "schedule not found"))
	}
	s.cron.Remove(id)
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"ok": true})
}
