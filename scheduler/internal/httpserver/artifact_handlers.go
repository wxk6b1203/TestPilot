package httpserver

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/testpilot/testpilot/internal/apperr"
	"github.com/testpilot/testpilot/internal/model"
)

// getArtifactContent 提供产物文件下载/预览（截图/trace/har 等）。
func (s *Server) getArtifactContent(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	id, ok := pathID(ctx, "id")
	if !ok {
		return nil
	}
	var art model.Artifact
	if err := s.db.Where("id = ? AND tenant_id = ?", id, c.TenantID).First(&art).Error; err != nil {
		return writeAppErr(ctx, apperr.NotFound(apperr.CodeNotFound, "artifact not found"))
	}
	// 产物根（Worker 与 Scheduler 须指向同一目录；生产换对象存储）
	root, err := filepath.Abs(s.cfg.ArtifactDir)
	if err != nil {
		return writeAppErr(ctx, apperr.Internal("artifact root resolve failed"))
	}
	path := filepath.Join(root, filepath.Clean("/"+art.URI)) // 剥前导..防穿越
	abs, err := filepath.Abs(path)
	if err != nil || !strings.HasPrefix(abs, root+string(os.PathSeparator)) {
		return writeAppErr(ctx, apperr.Forbidden(apperr.CodeForbidden, "artifact path escapes root"))
	}
	f, err := os.Open(abs)
	if err != nil {
		return writeAppErr(ctx, apperr.NotFound(apperr.CodeNotFound, "artifact file missing"))
	}
	// 不能 defer Close：fasthttp 在 handler 返回后才读流，读完自行 Close。

	switch strings.ToLower(filepath.Ext(abs)) {
	case ".png":
		ctx.Set(fiber.HeaderContentType, "image/png")
	case ".zip":
		ctx.Set(fiber.HeaderContentType, "application/zip")
	case ".har":
		ctx.Set(fiber.HeaderContentType, "application/json")
	default:
		ctx.Set(fiber.HeaderContentType, "application/octet-stream")
	}
	ctx.Set(fiber.HeaderContentDisposition,
		`inline; filename="`+strings.ReplaceAll(filepath.Base(abs), `"`, "")+`"`)
	size := -1
	if st, err := f.Stat(); err == nil {
		size = int(st.Size())
	}
	// 注：net/http 时期的 Range/Last-Modified 支持随 ServeContent 一并移除（预览场景无需）。
	return ctx.SendStream(f, size)
}
