package httpserver

import (
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

// ---- 证书管理（Phase 2 补齐）：CRUD 已落地；Worker 客户端证书执行（cert_ref/key_ref
// 解析与 TLS 绑定）依赖密钥后端，暂属另议项，见 docs/roadmap.md。 ----

func (s *Server) listCertificates(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	pid := queryInt(ctx, "project_id")
	return listOf[model.Certificate](s.db, ctx, func(q *gorm.DB) *gorm.DB {
		q = q.Where("tenant_id = ?", c.TenantID)
		if pid != 0 {
			q = q.Where("project_id = ?", pid)
		}
		return q
	})
}

func (s *Server) createCertificate(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	var in model.Certificate
	if !decode(ctx, &in) {
		return nil
	}
	if strings.TrimSpace(in.Name) == "" {
		return writeErr(ctx, fiber.StatusBadRequest, "name is required")
	}
	if in.Type == "" {
		in.Type = "pem"
	}
	if in.ProjectID == 0 {
		return writeErr(ctx, fiber.StatusBadRequest, "project_id is required")
	}
	if !ensureEntity(s.db, ctx, "project", in.ProjectID) {
		return nil
	}
	assignIDs(&in, c.TenantID)
	if err := s.db.Create(&in).Error; err != nil {
		return writeInternalErr(ctx, err)
	}
	return writeJSON(ctx, fiber.StatusOK, &in)
}

func (s *Server) getCertificate(ctx fiber.Ctx) error {
	return getOf[model.Certificate](s.db, ctx)
}

func (s *Server) updateCertificate(ctx fiber.Ctx) error {
	// 不允许通过 PUT 漂移租户/主键；project_id 变更会经 validateRefs 校验归属。
	return updateOf[model.Certificate](s.db, ctx)
}

func (s *Server) deleteCertificate(ctx fiber.Ctx) error {
	return deleteOf[model.Certificate](s.db, ctx)
}
