package httpserver

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/testpilot/testpilot/internal/apperr"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
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

// maxUploadBytes 与 binary_ref 内联上限对齐：超限产物无法被派发内联，入口即拒。
const maxUploadBytes = 8 << 20

// listArtifacts GET /api/v1/artifacts?kind=&run_id=（viewer）：产物清单，
// 回答"binary_ref 引用的文件在哪/有哪些产物"。
func (s *Server) listArtifacts(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	return listOf[model.Artifact](s.db, ctx, func(q *gorm.DB) *gorm.DB {
		q = q.Where("tenant_id = ?", c.TenantID)
		if k := queryInt(ctx, "kind"); k != 0 {
			q = q.Where("kind = ?", k)
		}
		if rid := queryInt(ctx, "run_id"); rid != 0 {
			q = q.Where("run_id = ?", rid)
		}
		return q.Order("id desc")
	})
}

// uploadArtifact POST /api/v1/artifacts（multipart: file；member）：用户上传二进制
// （binary_ref 供体等）。写 artifactRoot/uploads/<id>/<name> → Ingest（s3 上传+删本地，
// local 为 no-op，与 Worker 产物同一约定）→ 落 Artifact 行（run_id=0：retention 只清
// 已删运行的产物，上传不受影响）。
func (s *Server) uploadArtifact(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	fh, err := ctx.FormFile("file")
	if err != nil {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "multipart field 'file' is required"))
	}
	if fh.Size <= 0 || fh.Size > maxUploadBytes {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam,
			"file size must be within 1B..8MiB (binary_ref inline cap)"))
	}
	store := s.artifactStore()
	if store == nil {
		return writeAppErr(ctx, apperr.Internal("artifact store unavailable"))
	}

	// 文件名净化（剥路径 + 字符白名单，防穿越/防怪名）
	name := strings.ReplaceAll(fh.Filename, "\\", "/")
	if i := strings.LastIndexAny(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	name = strings.Trim(b.String(), ".")
	if name == "" {
		name = "upload.bin"
	}
	if len(name) > 120 {
		name = name[:120]
	}

	// 内容实测（fh.Size 可能不可信）：超限入口即拒
	src, err := fh.Open()
	if err != nil {
		return writeInternalErr(ctx, err)
	}
	defer src.Close() //nolint:errcheck
	data, err := io.ReadAll(io.LimitReader(src, maxUploadBytes+1))
	if err != nil {
		return writeInternalErr(ctx, err)
	}
	if int64(len(data)) == 0 || int64(len(data)) > maxUploadBytes {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam,
			"file size must be within 1B..8MiB (binary_ref inline cap)"))
	}

	artID := model.NextID()
	art := &model.Artifact{
		ID:       artID,
		TenantID: c.TenantID,
		Kind:     model.ArtifactKindUpload,
		URI:      fmt.Sprintf("uploads/%d/%s", artID, name),
		Size:     int64(len(data)),
	}
	dest := filepath.Join(s.cfg.ArtifactDir, filepath.Clean("/"+art.URI))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return writeInternalErr(ctx, err)
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return writeInternalErr(ctx, err)
	}
	// s3 后端：上传并删本地临时落盘；local：no-op（文件已在位）
	if err := store.Ingest(dest, c.TenantID, art.URI); err != nil {
		return writeInternalErr(ctx, err)
	}
	if err := s.db.Create(art).Error; err != nil {
		return writeInternalErr(ctx, err)
	}
	return writeJSON(ctx, fiber.StatusOK, art)
}
