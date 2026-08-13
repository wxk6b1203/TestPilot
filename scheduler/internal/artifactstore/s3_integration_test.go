package artifactstore

// S3 实网集成测试：默认跳过，仅当显式提供凭据时运行——
//
//	TP_TEST_S3=1 TP_TEST_S3_ENDPOINT=https://s3.cn-shanghai.aliyuncs.com \
//	TP_TEST_S3_AK=... TP_TEST_S3_SK=... TP_TEST_S3_BUCKET=bee-all \
//	go test ./internal/artifactstore/ -run TestS3Roundtrip -v
//
// 对象写在 testpilot-dev/<随机>/ 前缀下，测试结束自清理；不触碰 bucket 其他内容。

import (
	"fmt"
	"io"
	"math/rand"
	"os"
	"testing"
)

func s3TestEnv() (S3Config, string, bool) {
	if os.Getenv("TP_TEST_S3") != "1" {
		return S3Config{}, "", false
	}
	cfg := S3Config{
		Endpoint:  os.Getenv("TP_TEST_S3_ENDPOINT"),
		AccessKey: os.Getenv("TP_TEST_S3_AK"),
		SecretKey: os.Getenv("TP_TEST_S3_SK"),
		Bucket:    os.Getenv("TP_TEST_S3_BUCKET"),
		Region:    os.Getenv("TP_TEST_S3_REGION"),
		UseSSL:    true,
	}
	if cfg.Endpoint == "" || cfg.AccessKey == "" || cfg.Bucket == "" {
		return S3Config{}, "", false
	}
	return cfg, fmt.Sprintf("testpilot-dev/%d/", rand.Int63()), true
}

func TestS3Roundtrip(t *testing.T) {
	cfg, prefix, ok := s3TestEnv()
	if !ok {
		t.Skip("TP_TEST_S3=1 + endpoint/ak/bucket env required")
	}
	cfg.Prefix = prefix
	s, err := NewS3(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Ingest：本地文件上传后本地删除
	src := t.TempDir()
	local := src + "/shot.png"
	if err := os.WriteFile(local, []byte("s3-roundtrip-data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Ingest(local, 42, "runs/1/shot.png"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(local); !os.IsNotExist(err) {
		t.Fatal("Ingest should remove local file after upload")
	}

	// Open 读回
	obj, err := s.Open(42, "runs/1/shot.png")
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(obj)
	obj.Close()
	if err != nil || string(b) != "s3-roundtrip-data" {
		t.Fatalf("read got %q err=%v", b, err)
	}

	// 租户隔离：别的租户读不到（S3 GetObject 惰性，404 在首次 Read 暴露）
	obj2, err := s.Open(43, "runs/1/shot.png")
	if err == nil {
		_, rerr := io.ReadAll(obj2)
		obj2.Close()
		if rerr == nil {
			t.Fatal("cross-tenant read should fail")
		}
	}

	// Delete
	if err := s.Delete(42, "runs/1/shot.png"); err != nil {
		t.Fatal(err)
	}
	obj3, err := s.Open(42, "runs/1/shot.png")
	if err == nil {
		_, rerr := io.ReadAll(obj3)
		obj3.Close()
		if rerr == nil {
			t.Fatal("object should be deleted")
		}
	}
}
