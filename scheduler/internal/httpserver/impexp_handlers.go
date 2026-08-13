package httpserver

import (
	"encoding/json"

	"github.com/gofiber/fiber/v3"
	"github.com/testpilot/testpilot/internal/apperr"
	"github.com/testpilot/testpilot/internal/impexp"
	"github.com/testpilot/testpilot/internal/model"
)

// ---- 导入导出 ----

type importOpenAPIReq struct {
	ProjectID    int64           `json:"project_id"`
	Document     json.RawMessage `json:"document"`      // JSON 对象形式
	DocumentYAML string          `json:"document_yaml"` // YAML 字符串形式
}

func (s *Server) importOpenAPI(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	var in importOpenAPIReq
	if !decode(ctx, &in) {
		return nil
	}
	if in.ProjectID == 0 {
		return writeErr(ctx, fiber.StatusBadRequest, "project_id required")
	}
	doc := []byte(in.Document)
	if len(doc) == 0 {
		doc = []byte(in.DocumentYAML)
	}
	if len(doc) == 0 {
		return writeErr(ctx, fiber.StatusBadRequest, "document or document_yaml required")
	}
	res, err := impexp.ImportOpenAPI(s.db, c.TenantID, in.ProjectID, doc)
	if err != nil {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeImportParse, err.Error()))
	}
	return writeJSON(ctx, fiber.StatusOK, res)
}

type importCurlReq struct {
	ProjectID int64  `json:"project_id"`
	Command   string `json:"command"`
}

func (s *Server) importCurl(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	var in importCurlReq
	if !decode(ctx, &in) {
		return nil
	}
	if in.ProjectID == 0 || in.Command == "" {
		return writeErr(ctx, fiber.StatusBadRequest, "project_id and command required")
	}
	id, err := impexp.ImportCurl(s.db, c.TenantID, in.ProjectID, in.Command)
	if err != nil {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeImportParse, err.Error()))
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"api_id": id})
}

func (s *Server) exportOpenAPI(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	pid := queryInt(ctx, "project_id")
	if pid == 0 {
		return writeErr(ctx, fiber.StatusBadRequest, "project_id required")
	}
	title := "testpilot export"
	var p model.Project
	if err := s.db.Select("name").Where("id = ? AND tenant_id = ?", pid, c.TenantID).
		First(&p).Error; err == nil {
		title = p.Name
	}
	doc, err := impexp.ExportOpenAPI(s.db, c.TenantID, pid, title)
	if err != nil {
		return writeErr(ctx, fiber.StatusInternalServerError, err.Error())
	}
	ctx.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	ctx.Set(fiber.HeaderContentDisposition, `attachment; filename="openapi.json"`)
	return ctx.Status(fiber.StatusOK).Send(doc)
}
