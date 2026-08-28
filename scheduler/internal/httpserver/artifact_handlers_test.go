package httpserver

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/testpilot/testpilot/internal/auth"
	"github.com/testpilot/testpilot/internal/config"
	"github.com/testpilot/testpilot/internal/model"
)

func postMultipart(t *testing.T, app *fiber.App, path, jwt, field, filename string, content []byte) (int, map[string]any) {
	t.Helper()
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	part, err := w.CreateFormFile(field, filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", path, body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+jwt)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode response %q: %v", raw, err)
	}
	return resp.StatusCode, out
}

func TestArtifactUploadListDownload(t *testing.T) {
	cfg := config.Defaults()
	cfg.ArtifactDir = t.TempDir()
	app, d := newTestApp(t, cfg)

	var user model.User
	if err := d.Where("username = ?", "admin").First(&user).Error; err != nil {
		t.Fatal(err)
	}
	jwt, err := auth.IssueToken(cfg.JWTSecret, user.ID, 1, auth.RoleOwner, 1)
	if err != nil {
		t.Fatal(err)
	}

	// 上传 → 200，kind=upload，uri 含净化文件名
	content := []byte("binary-payload-\x00\x01\x02")
	code, out := postMultipart(t, app, "/api/v1/artifacts", jwt, "file", "测试 文件~1.bin", content)
	if code != 200 {
		t.Fatalf("upload: %d %v", code, out)
	}
	id, _ := out["id"].(string)
	if id == "" {
		t.Fatalf("no id: %v", out)
	}
	if int(out["kind"].(float64)) != int(model.ArtifactKindUpload) {
		t.Fatalf("kind=%v", out["kind"])
	}
	uri, _ := out["uri"].(string)
	if !filepath.HasPrefix(uri, "uploads/") || !bytes.Contains([]byte(uri), []byte("bin")) {
		t.Fatalf("uri=%q", uri)
	}

	// 列表可见 + kind 过滤
	code, out = getJSON(t, app, "/api/v1/artifacts?kind=9", jwt)
	if code != 200 || len(out["items"].([]any)) != 1 {
		t.Fatalf("list: %d %v", code, out)
	}
	code, out = getJSON(t, app, "/api/v1/artifacts?kind=1", jwt)
	if code != 200 || len(out["items"].([]any)) != 0 {
		t.Fatalf("list filter: %d %v", code, out)
	}

	// 下载内容一致
	req := httptest.NewRequest("GET", "/api/v1/artifacts/"+id+"/content", nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Equal(got, content) {
		t.Fatalf("download mismatch: %q", got)
	}

	// 文件名净化：路径穿越被剥离
	code, out = postMultipart(t, app, "/api/v1/artifacts", jwt, "file", "../../evil.bin", []byte("x"))
	if code != 200 {
		t.Fatalf("upload traversal: %d %v", code, out)
	}
	if uri2, _ := out["uri"].(string); bytes.Contains([]byte(uri2), []byte("..")) {
		t.Fatalf("uri must not contain ..: %q", uri2)
	}
}

func TestArtifactUploadAuthAndLimits(t *testing.T) {
	cfg := config.Defaults()
	cfg.ArtifactDir = t.TempDir()
	app, d := newTestApp(t, cfg)

	var user model.User
	if err := d.Where("username = ?", "admin").First(&user).Error; err != nil {
		t.Fatal(err)
	}
	ownerJWT, _ := auth.IssueToken(cfg.JWTSecret, user.ID, 1, auth.RoleOwner, 1)
	viewerJWT, _ := auth.IssueToken(cfg.JWTSecret, user.ID, 1, auth.RoleViewer, 1)

	// viewer 不能上传
	code, _ := postMultipart(t, app, "/api/v1/artifacts", viewerJWT, "file", "a.bin", []byte("x"))
	if code != fiber.StatusForbidden {
		t.Fatalf("viewer upload: want 403, got %d", code)
	}

	// 超限（>8MiB）→ 400
	code, _ = postMultipart(t, app, "/api/v1/artifacts", ownerJWT, "file", "big.bin", make([]byte, 9<<20))
	if code != fiber.StatusBadRequest {
		t.Fatalf("oversize: want 400, got %d", code)
	}

	// 缺 file 字段 → 400
	code, _ = postMultipart(t, app, "/api/v1/artifacts", ownerJWT, "not-file", "a.bin", []byte("x"))
	if code != fiber.StatusBadRequest {
		t.Fatalf("missing field: want 400, got %d", code)
	}
}
