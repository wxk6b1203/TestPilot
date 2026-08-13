package httpserver

import (
	"github.com/gofiber/fiber/v3"
	"strconv"

	"github.com/testpilot/testpilot/internal/apperr"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

// ---- 通知渠道 CRUD（admin）。type: 1=webhook 2=dingtalk 3=feishu ----

func (s *Server) listNotificationChannels(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	return listOf[model.NotificationChannel](s.db, ctx, func(q *gorm.DB) *gorm.DB {
		return q.Where("tenant_id = ?", c.TenantID).Order("id desc")
	})
}

type channelReq struct {
	Name    string `json:"name"`
	Type    int16  `json:"type"`
	URL     string `json:"url"`
	Secret  string `json:"secret"`
	Events  string `json:"events"`
	Enabled *bool  `json:"enabled"`
}

func (s *Server) createNotificationChannel(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	var in channelReq
	if !decode(ctx, &in) {
		return nil
	}
	if in.URL == "" || in.Type < 1 || in.Type > 3 {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "url 必填，type ∈ {1 webhook,2 dingtalk,3 feishu}"))
	}
	if in.Events == "" {
		in.Events = "run_finished,stress_finished"
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	row := &model.NotificationChannel{
		ID: model.NextID(), TenantID: c.TenantID, Name: in.Name, Type: in.Type,
		URL: in.URL, Secret: in.Secret, Events: in.Events, Enabled: enabled,
	}
	if err := s.db.Create(row).Error; err != nil {
		return writeAppErr(ctx, apperr.Internal(err.Error()))
	}
	return writeJSON(ctx, fiber.StatusOK, row)
}

func (s *Server) updateNotificationChannel(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "invalid id"))
	}
	var row model.NotificationChannel
	if err := s.db.Where("id = ? AND tenant_id = ?", id, c.TenantID).First(&row).Error; err != nil {
		return writeAppErr(ctx, apperr.NotFound(apperr.CodeNotFound, "channel not found"))
	}
	var in channelReq
	if !decode(ctx, &in) {
		return nil
	}
	if in.Name != "" {
		row.Name = in.Name
	}
	if in.Type >= 1 && in.Type <= 3 {
		row.Type = in.Type
	}
	if in.URL != "" {
		row.URL = in.URL
	}
	if in.Secret != "" {
		row.Secret = in.Secret
	}
	if in.Events != "" {
		row.Events = in.Events
	}
	if in.Enabled != nil {
		row.Enabled = *in.Enabled
	}
	if err := s.db.Save(&row).Error; err != nil {
		return writeAppErr(ctx, apperr.Internal(err.Error()))
	}
	return writeJSON(ctx, fiber.StatusOK, row)
}

func (s *Server) deleteNotificationChannel(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "invalid id"))
	}
	res := s.db.Where("id = ? AND tenant_id = ?", id, c.TenantID).Delete(&model.NotificationChannel{})
	if res.RowsAffected == 0 {
		return writeAppErr(ctx, apperr.NotFound(apperr.CodeNotFound, "channel not found"))
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"ok": true})
}
