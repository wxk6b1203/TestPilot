package httpserver

import (
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/testpilot/testpilot/internal/apperr"
	"github.com/testpilot/testpilot/internal/model"
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
		return q.Where("tenant_id = ? AND user_id = ? AND deleted_at IS NULL", c.TenantID, c.UserID).Order("id desc")
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

// updateCopilotSession 重命名会话：仅改标题，消息与回放不受影响。
func (s *Server) updateCopilotSession(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	sid, ok := pathID(ctx, "id")
	if !ok {
		return nil
	}
	var in sessionReq
	if !decode(ctx, &in) {
		return nil
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "标题不能为空"))
	}
	res := s.db.Model(&model.CopilotSession{}).
		Where("id = ? AND tenant_id = ? AND user_id = ? AND deleted_at IS NULL",
			sid, c.TenantID, c.UserID).
		Update("title", title)
	if res.Error != nil {
		return writeAppErr(ctx, apperr.Internal(res.Error.Error()))
	}
	if res.RowsAffected == 0 {
		// MySQL 对同值 UPDATE 上报 0 行受影响：区分「改名成同名」（200）与
		// 「会话不存在/不可见」（404）
		var cnt int64
		s.db.Model(&model.CopilotSession{}).
			Where("id = ? AND tenant_id = ? AND user_id = ? AND deleted_at IS NULL",
				sid, c.TenantID, c.UserID).Count(&cnt)
		if cnt == 0 {
			return writeAppErr(ctx, apperr.NotFound(apperr.CodeNotFound, "session not found"))
		}
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"id": sid, "title": title})
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
	if err := s.db.Where("id = ? AND tenant_id = ? AND user_id = ? AND deleted_at IS NULL",
		sid, c.TenantID, c.UserID).First(&sess).Error; err != nil {
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
	if err := s.db.Where("id = ? AND tenant_id = ? AND user_id = ? AND deleted_at IS NULL",
		sid, c.TenantID, c.UserID).First(&sess).Error; err != nil {
		return writeAppErr(ctx, apperr.NotFound(apperr.CodeNotFound, "session not found"))
	}
	var in messageReq
	if !decode(ctx, &in) {
		return nil
	}
	// 注意：用户消息的 ai_calls 计费已上移到 chat 代理层（/copilot-api/chat）——
	// Copilot 按内容去重会跳过本 POST，在持久化处扣费可被重复消息绕过。
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

// ---- 回收站：软删除 / 回收站列表 / 彻底删除 ----

// deleteCopilotSession 软删除会话：deleted_at 非空即进入回收站。
func (s *Server) deleteCopilotSession(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	sid, ok := pathID(ctx, "id")
	if !ok {
		return nil
	}
	now := time.Now()
	res := s.db.Model(&model.CopilotSession{}).
		Where("id = ? AND tenant_id = ? AND user_id = ? AND deleted_at IS NULL",
			sid, c.TenantID, c.UserID).
		Update("deleted_at", now)
	if res.Error != nil {
		return writeAppErr(ctx, apperr.Internal(res.Error.Error()))
	}
	if res.RowsAffected == 0 {
		return writeAppErr(ctx, apperr.NotFound(apperr.CodeNotFound, "session not found"))
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"id": sid, "deleted_at": now})
}

type trashSession struct {
	ID           int64     `json:"id"`
	Title        string    `json:"title"`
	CreatedAt    time.Time `json:"created_at"`
	DeletedAt    time.Time `json:"deleted_at"`
	MessageCount int64     `json:"message_count"`
}

// listCopilotTrash 当前用户的回收站列表（按删除时间倒序）。
func (s *Server) listCopilotTrash(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	var total int64
	base := s.db.Model(&model.CopilotSession{}).
		Where("tenant_id = ? AND user_id = ? AND deleted_at IS NOT NULL", c.TenantID, c.UserID)
	if err := base.Count(&total).Error; err != nil {
		return writeInternalErr(ctx, err)
	}
	rows := make([]model.CopilotSession, 0)
	offset, limit := pageParams(ctx)
	if err := base.Order("deleted_at desc").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return writeInternalErr(ctx, err)
	}
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	counts := map[int64]int64{}
	if len(ids) > 0 {
		type msgCount struct {
			SessionID int64
			N         int64
		}
		var cc []msgCount
		if err := s.db.Model(&model.CopilotMessage{}).
			Select("session_id, COUNT(*) AS n").
			Where("session_id IN ?", ids).
			Group("session_id").
			Scan(&cc).Error; err != nil {
			return writeInternalErr(ctx, err)
		}
		for _, x := range cc {
			counts[x.SessionID] = x.N
		}
	}
	items := make([]trashSession, 0, len(rows))
	for _, r := range rows {
		if r.DeletedAt == nil {
			continue
		}
		items = append(items, trashSession{
			ID:           r.ID,
			Title:        r.Title,
			CreatedAt:    r.CreatedAt,
			DeletedAt:    *r.DeletedAt,
			MessageCount: counts[r.ID],
		})
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"items": items, "total": total})
}

// purgeCopilotTrash 手动彻底删除回收站中的一条会话（连同其消息）。
func (s *Server) purgeCopilotTrash(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	sid, ok := pathID(ctx, "id")
	if !ok {
		return nil
	}
	var sess model.CopilotSession
	if err := s.db.Where("id = ? AND tenant_id = ? AND user_id = ? AND deleted_at IS NOT NULL",
		sid, c.TenantID, c.UserID).First(&sess).Error; err != nil {
		return writeAppErr(ctx, apperr.NotFound(apperr.CodeNotFound, "trash session not found"))
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("session_id = ? AND tenant_id = ?", sid, c.TenantID).
			Delete(&model.CopilotMessage{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", sid).Delete(&model.CopilotSession{}).Error
	})
	if err != nil {
		return writeAppErr(ctx, apperr.Internal(err.Error()))
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"deleted": true})
}

func (s *Server) listAuditLogs(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	return listOf[model.AuditLog](s.db, ctx, func(q *gorm.DB) *gorm.DB {
		return q.Where("tenant_id = ?", c.TenantID).Order("id desc")
	})
}
