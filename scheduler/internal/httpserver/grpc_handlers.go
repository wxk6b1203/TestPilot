package httpserver

import (
	"github.com/gofiber/fiber/v3"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

// ---- gRPC 接口与 proto 文件（v2 第三批） ----

func (s *Server) listGrpcAPIs(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	pid := queryInt(ctx, "project_id")
	return listOf[model.GrpcApi](s.db, ctx, func(q *gorm.DB) *gorm.DB {
		q = q.Where("tenant_id = ?", c.TenantID)
		if pid != 0 {
			q = q.Where("project_id = ?", pid)
		}
		return q
	})
}

func (s *Server) createGrpcAPI(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	var in model.GrpcApi
	if !decode(ctx, &in) {
		return nil
	}
	if in.FullService == "" || in.Method == "" {
		return writeErr(ctx, fiber.StatusBadRequest, "full_service and method required")
	}
	assignIDs(&in, c.TenantID)
	if err := s.db.Create(&in).Error; err != nil {
		return writeErr(ctx, fiber.StatusInternalServerError, err.Error())
	}
	return writeJSON(ctx, fiber.StatusOK, &in)
}

func (s *Server) getGrpcAPI(ctx fiber.Ctx) error    { return getOf[model.GrpcApi](s.db, ctx) }
func (s *Server) updateGrpcAPI(ctx fiber.Ctx) error { return updateOf[model.GrpcApi](s.db, ctx) }
func (s *Server) deleteGrpcAPI(ctx fiber.Ctx) error { return deleteOf[model.GrpcApi](s.db, ctx) }

func (s *Server) listProtoFiles(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	pid := queryInt(ctx, "project_id")
	return listOf[model.ProtoFile](s.db, ctx, func(q *gorm.DB) *gorm.DB {
		q = q.Where("tenant_id = ?", c.TenantID)
		if pid != 0 {
			q = q.Where("project_id = ?", pid)
		}
		return q
	})
}

func (s *Server) createProtoFile(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	var in model.ProtoFile
	if !decode(ctx, &in) {
		return nil
	}
	if in.Filename == "" || in.Content == "" {
		return writeErr(ctx, fiber.StatusBadRequest, "filename and content required")
	}
	assignIDs(&in, c.TenantID)
	if err := s.db.Create(&in).Error; err != nil {
		return writeErr(ctx, fiber.StatusInternalServerError, err.Error())
	}
	return writeJSON(ctx, fiber.StatusOK, &in)
}

func (s *Server) getProtoFile(ctx fiber.Ctx) error    { return getOf[model.ProtoFile](s.db, ctx) }
func (s *Server) updateProtoFile(ctx fiber.Ctx) error { return updateOf[model.ProtoFile](s.db, ctx) }
func (s *Server) deleteProtoFile(ctx fiber.Ctx) error { return deleteOf[model.ProtoFile](s.db, ctx) }
