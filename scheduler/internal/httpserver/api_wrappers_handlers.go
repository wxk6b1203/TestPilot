package httpserver

import (
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/testpilot/testpilot/internal/apperr"
	"github.com/testpilot/testpilot/internal/model"
)

// projectAPIWrappers 预览项目接口的低代码封装源码（GET /projects/:id/api-wrappers）。
// 不带过滤参数时生成项目全部 HTTP/gRPC 接口；http_ids/grpc_ids 逗号分隔限定接口。
func (s *Server) projectAPIWrappers(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	pid, ok := pathID(ctx, "id")
	if !ok {
		return nil
	}
	var proj model.Project
	if err := s.db.Where("id = ? AND tenant_id = ?", pid, c.TenantID).First(&proj).Error; err != nil {
		return writeAppErr(ctx, apperr.NotFound(apperr.CodeNotFound, "project not found"))
	}
	httpIDs := splitCSV(ctx.Query("http_ids"))
	grpcIDs := splitCSV(ctx.Query("grpc_ids"))
	source, count, err := s.run.PreviewAPIWrappers(c.TenantID, pid, httpIDs, grpcIDs)
	if err != nil {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, err.Error()))
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{
		"source": source,
		"count":  count,
	})
}

func splitCSV(s string) []string {
	out := []string{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
