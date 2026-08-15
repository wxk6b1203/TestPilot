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
		return writeInternalErr(ctx, err)
	}
	ctx.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	ctx.Set(fiber.HeaderContentDisposition, `attachment; filename="openapi.json"`)
	return ctx.Status(fiber.StatusOK).Send(doc)
}

type importPostmanReq struct {
	ProjectID int64           `json:"project_id"`
	Document  json.RawMessage `json:"document"` // Postman Collection v2.1 JSON
}

func (s *Server) importPostman(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	var in importPostmanReq
	if !decode(ctx, &in) {
		return nil
	}
	if in.ProjectID == 0 || len(in.Document) == 0 {
		return writeErr(ctx, fiber.StatusBadRequest, "project_id and document required")
	}
	res, err := impexp.ImportPostman(s.db, c.TenantID, in.ProjectID, in.Document)
	if err != nil {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeImportParse, err.Error()))
	}
	return writeJSON(ctx, fiber.StatusOK, res)
}

func (s *Server) exportPostman(ctx fiber.Ctx) error {
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
	doc, err := impexp.ExportPostman(s.db, c.TenantID, pid, title)
	if err != nil {
		return writeInternalErr(ctx, err)
	}
	ctx.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	ctx.Set(fiber.HeaderContentDisposition, `attachment; filename="collection.json"`)
	return ctx.Status(fiber.StatusOK).Send(doc)
}

func (s *Server) exportCurl(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	pid := queryInt(ctx, "project_id")
	if pid == 0 {
		return writeErr(ctx, fiber.StatusBadRequest, "project_id required")
	}
	out, err := impexp.ExportCurl(s.db, c.TenantID, pid)
	if err != nil {
		return writeInternalErr(ctx, err)
	}
	ctx.Set(fiber.HeaderContentType, "text/plain; charset=utf-8")
	ctx.Set(fiber.HeaderContentDisposition, `attachment; filename="apis.sh"`)
	return ctx.Status(fiber.StatusOK).SendString(out)
}
