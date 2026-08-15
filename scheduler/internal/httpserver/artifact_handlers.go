package httpserver

import (
	"io"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/testpilot/testpilot/internal/apperr"
	"github.com/testpilot/testpilot/internal/model"
)

// selfClosingReader 读尽（EOF/出错/读满 size）时自动 Close 底层流：
// fasthttp 在 handler 返回后才消费流，无法 defer Close；S3 连接必须显式释放。
// 注意：fasthttp 按 Size 精确读完后不再调用 Read（无 EOF），
// 因此必须以 remaining 计数在"读满 size"时主动释放，否则句柄/S3 连接常态泄漏。
type selfClosingReader struct {
	r         io.ReadCloser
	remaining int64
	closed    bool
}

func (s *selfClosingReader) Read(p []byte) (int, error) {
	if s.closed {
		return 0, io.EOF
	}
	n, err := s.r.Read(p)
	if n > 0 {
		s.remaining -= int64(n)
	}
	if err != nil || s.remaining <= 0 {
		s.closed = true
		_ = s.r.Close()
		if err == nil {
			// 读满 size：以 EOF 优雅结束——fasthttp 读满后还会再调一次 Read
			// 确认流结束，若底层已被关闭会报错并中断连接（RemoteProtocolError）
			err = io.EOF
		}
	}
	return n, err
}

// getArtifactContent 提供产物文件下载/预览（截图/trace/har 等）。
// 内容经产物后端读取（local 本地目录 / s3 对象存储），路径穿越防护由后端负责。
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
	store := s.artifactStore()
	if store == nil {
		return writeAppErr(ctx, apperr.Internal("artifact store unavailable"))
	}
	f, err := store.Open(art.TenantID, art.URI)
	if err != nil {
		return writeAppErr(ctx, apperr.NotFound(apperr.CodeNotFound, "artifact file missing"))
	}
	// 不能 defer Close：fasthttp 在 handler 返回后才读流；selfClosingReader 在 EOF 自关。

	name := art.URI
	if i := strings.LastIndexAny(name, "/\\"); i >= 0 {
		name = name[i+1:]
	}
	switch {
	case strings.HasSuffix(strings.ToLower(name), ".png"):
		ctx.Set(fiber.HeaderContentType, "image/png")
	case strings.HasSuffix(strings.ToLower(name), ".zip"):
		ctx.Set(fiber.HeaderContentType, "application/zip")
	case strings.HasSuffix(strings.ToLower(name), ".har"):
		ctx.Set(fiber.HeaderContentType, "application/json")
	default:
		ctx.Set(fiber.HeaderContentType, "application/octet-stream")
	}
	ctx.Set(fiber.HeaderContentDisposition,
		`inline; filename="`+strings.ReplaceAll(name, `"`, "")+`"`)
	return ctx.SendStream(&selfClosingReader{r: f, remaining: art.Size}, int(art.Size))
}
