package httpserver

import (
	"github.com/gofiber/fiber/v3"

	"github.com/testpilot/testpilot/internal/apperr"
	"github.com/testpilot/testpilot/internal/model"
	"github.com/testpilot/testpilot/internal/quota"
	"gorm.io/gorm"
)

// ---- Copilot 会话持久化 / 审计 ----
// Copilot 服务以前端用户的 Bearer token 调这些接口落库（不直连 DB）。

type sessionReq struct {
	Title string `json:"title"`
}

func (s *Server) listCopilotSessions(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	return listOf[model.CopilotSession](s.db, ctx, func(q *gorm.DB) *gorm.DB {
		return q.Where("tenant_id = ? AND user_id = ?", c.TenantID, c.UserID).Order("id desc")
	})
}

func (s *Server) createCopilotSession(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	var in sessionReq
	if !decode(ctx, &in) {
		return nil
	}
	row := &model.CopilotSession{
		ID:       model.NextID(),
		TenantID: c.TenantID,
		UserID:   c.UserID,
		Title:    in.Title,
	}
	if err := s.db.Create(row).Error; err != nil {
		return writeAppErr(ctx, apperr.Internal(err.Error()))
	}
	return writeJSON(ctx, fiber.StatusOK, row)
}

type messageReq struct {
	Role      int16      `json:"role"`
	Content   string     `json:"content"`
	ToolCalls model.JSON `json:"tool_calls"`
}

func (s *Server) listCopilotMessages(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	sid, ok := pathID(ctx, "id")
	if !ok {
		return nil
	}
	var sess model.CopilotSession
	if err := s.db.Where("id = ? AND tenant_id = ? AND user_id = ?", sid, c.TenantID, c.UserID).First(&sess).Error; err != nil {
		return writeAppErr(ctx, apperr.NotFound(apperr.CodeNotFound, "session not found"))
	}
	rows := make([]model.CopilotMessage, 0)
	s.db.Where("session_id = ? AND tenant_id = ?", sid, c.TenantID).Order("id asc").Find(&rows)
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"items": rows, "total": len(rows)})
}

func (s *Server) appendCopilotMessage(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	sid, ok := pathID(ctx, "id")
	if !ok {
		return nil
	}
	var sess model.CopilotSession
	if err := s.db.Where("id = ? AND tenant_id = ? AND user_id = ?", sid, c.TenantID, c.UserID).First(&sess).Error; err != nil {
		return writeAppErr(ctx, apperr.NotFound(apperr.CodeNotFound, "session not found"))
	}
	var in messageReq
	if !decode(ctx, &in) {
		return nil
	}
	// 用户消息计 ai_calls 配额（与 Copilot 工具面一致；超限 429）
	if in.Role == 1 {
		if err := quota.Check(s.db, c.TenantID, quota.MetricAICalls, 1); err != nil {
			return writeAppErr(ctx, err)
		}
	}
	row := &model.CopilotMessage{
		ID:        model.NextID(),
		TenantID:  c.TenantID,
		SessionID: sid,
		Role:      in.Role,
		Content:   in.Content,
		ToolCalls: in.ToolCalls,
	}
	if err := s.db.Create(row).Error; err != nil {
		return writeAppErr(ctx, apperr.Internal(err.Error()))
	}
	// 首条用户消息自动生成会话标题
	if sess.Title == "" && in.Role == 1 && in.Content != "" {
		title := []rune(in.Content)
		if len(title) > 40 {
			title = title[:40]
		}
		s.db.Model(&sess).Update("title", string(title))
	}
	return writeJSON(ctx, fiber.StatusOK, row)
}

func (s *Server) listAuditLogs(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	return listOf[model.AuditLog](s.db, ctx, func(q *gorm.DB) *gorm.DB {
		return q.Where("tenant_id = ?", c.TenantID).Order("id desc")
	})
}
