package httpserver

import (
	"github.com/gofiber/fiber/v3"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

// ---- 脚本资产库（LowCodeCase.script_ref 引用目标） ----

func (s *Server) listScripts(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	pid := queryInt(ctx, "project_id")
	return listOf[model.Script](s.db, ctx, func(q *gorm.DB) *gorm.DB {
		q = q.Where("tenant_id = ?", c.TenantID)
		if pid != 0 {
			q = q.Where("project_id = ?", pid)
		}
		return q
	})
}

func (s *Server) createScript(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	var in model.Script
	if !decode(ctx, &in) {
		return nil
	}
	if in.Content == "" {
		return writeErr(ctx, fiber.StatusBadRequest, "content is required")
	}
	assignIDs(&in, c.TenantID)
	if in.Language == "" {
		in.Language = "python"
	}
	if err := s.db.Create(&in).Error; err != nil {
		return writeErr(ctx, fiber.StatusInternalServerError, err.Error())
	}
	return writeJSON(ctx, fiber.StatusOK, &in)
}

func (s *Server) getScript(ctx fiber.Ctx) error    { return getOf[model.Script](s.db, ctx) }
func (s *Server) updateScript(ctx fiber.Ctx) error { return updateOf[model.Script](s.db, ctx) }
func (s *Server) deleteScript(ctx fiber.Ctx) error { return deleteOf[model.Script](s.db, ctx) }
