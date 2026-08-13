package artifactstore

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/testpilot/testpilot/internal/config"
)

// TestLocalOpenDelete 本地后端：读写删除 + 路径穿越防护。
func TestLocalOpenDelete(t *testing.T) {
	root := t.TempDir()
	l, err := NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "runs", "1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "runs", "1", "a.png"), []byte("png-data"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 正常读
	f, err := l.Open(1, "runs/1/a.png")
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(f)
	f.Close()
	if err != nil || string(b) != "png-data" {
		t.Fatalf("read got %q err=%v", b, err)
	}

	// 穿越不可达：URI 经 Clean("/"+uri) 锚定归一化，`..` 只能落在根内；
	// 根外的同名文件不受任何影响
	outside := filepath.Join(filepath.Dir(root), "outside-file")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Open(1, "../../outside-file"); err == nil {
		t.Fatal("normalized path shouldn't exist inside root")
	}
	if err := l.Delete(1, "../../outside-file"); err != nil {
		t.Fatal(err) // 归一化为 root/outside-file，不存在 → 幂等 no-op
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("file outside root must not be touched")
	}

	// 删除（幂等）
	if err := l.Delete(1, "runs/1/a.png"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "runs", "1", "a.png")); !os.IsNotExist(err) {
		t.Fatal("file should be removed")
	}
	if err := l.Delete(1, "runs/1/a.png"); err != nil {
		t.Fatalf("delete should be idempotent: %v", err)
	}

	// Ingest 为 no-op（local 即存储本身）
	if err := l.Ingest(filepath.Join(root, "runs/1/a.png"), 1, "runs/1/a.png"); err != nil {
		t.Fatal(err)
	}
}

// TestS3Key 对象键映射：前缀 + 租户 + 归一化 URI。
func TestS3Key(t *testing.T) {
	s := &S3{prefix: "tp/dev/"}
	if got := s.key(7, "runs/1/x.png"); got != "tp/dev/7/runs/1/x.png" {
		t.Fatalf("key=%q", got)
	}
	if got := s.key(7, "/runs/1/x.png"); got != "tp/dev/7/runs/1/x.png" {
		t.Fatalf("leading slash should be stripped: %q", got)
	}
	s2 := &S3{}
	if got := s2.key(7, "a.png"); got != "7/a.png" {
		t.Fatalf("no prefix key=%q", got)
	}
}

// TestNewUnknownBackend 未知后端报错。
func TestNewUnknownBackend(t *testing.T) {
	cfg := config.Defaults()
	cfg.ArtifactBackend = "oss"
	if _, err := New(cfg); err == nil || !strings.Contains(err.Error(), "unknown artifact_backend") {
		t.Fatalf("want unknown backend error, got %v", err)
	}
}

// TestNewS3Validation 缺必填项报错。
func TestNewS3Validation(t *testing.T) {
	if _, err := NewS3(S3Config{}); err == nil {
		t.Fatal("empty config should fail")
	}
	if _, err := NewS3(S3Config{Endpoint: "://bad", Bucket: "b", AccessKey: "a"}); err == nil {
		t.Fatal("invalid endpoint should fail")
	}
}
