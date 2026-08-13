// Package artifactstore 产物存储后端抽象：local（本地文件系统，默认）与 s3
// （S3 兼容对象存储，经 minio-go；Aliyun OSS S3 端点可用）。
//
// URI 是 Worker 上报的相对路径（如 runs/…/shot.png）；local 后端将其解析到
// 产物根目录并防路径穿越；s3 后端映射为 {prefix}{tenantID}/{uri} 对象键，
// 租户在键内隔离。写入路径：Worker 仍先写共享产物目录，Scheduler 收到
// TaskResult 后经 Ingest 上传（s3 上传成功后删除本地文件）。
package artifactstore

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/testpilot/testpilot/internal/config"
	"github.com/testpilot/testpilot/internal/logging"
)

// Backend 产物存取接口。
type Backend interface {
	// Open 打开产物读流（调用方在 EOF/出错后 Close；local 为 os.File）。
	Open(tenantID int64, uri string) (io.ReadCloser, error)
	// Delete 删除产物（retention 清理用；幂等：不存在不报错）。
	Delete(tenantID int64, uri string) error
	// Ingest 把本地文件同步到后端（local 为 no-op；s3 上传成功后删本地文件）。
	Ingest(localPath string, tenantID int64, uri string) error
}

// New 按配置构造后端；未知 backend 报错。
func New(cfg config.Config) (Backend, error) {
	switch strings.ToLower(cfg.ArtifactBackend) {
	case "", "local":
		return NewLocal(cfg.ArtifactDir)
	case "s3":
		return NewS3(S3Config{
			Endpoint: cfg.S3Endpoint, AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey,
			Bucket: cfg.S3Bucket, Region: cfg.S3Region, Prefix: cfg.S3Prefix,
			UseSSL: cfg.S3UseSSL, PathStyle: cfg.S3PathStyle,
		})
	default:
		return nil, fmt.Errorf("unknown artifact_backend %q (want local|s3)", cfg.ArtifactBackend)
	}
}

// ---- local ----

// Local 本地文件系统后端（默认；与历史行为一致）。
type Local struct {
	root string
}

func NewLocal(dir string) (*Local, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("artifact dir resolve: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("artifact dir create: %w", err)
	}
	return &Local{root: abs}, nil
}

// resolve 解析 URI 为根内绝对路径（剥前导 .. 防穿越）。
func (l *Local) resolve(uri string) (string, error) {
	path := filepath.Join(l.root, filepath.Clean("/"+uri))
	abs, err := filepath.Abs(path)
	if err != nil || !strings.HasPrefix(abs, l.root+string(os.PathSeparator)) {
		return "", fmt.Errorf("artifact uri %q escapes root", uri)
	}
	return abs, nil
}

func (l *Local) Open(_ int64, uri string) (io.ReadCloser, error) {
	abs, err := l.resolve(uri)
	if err != nil {
		return nil, err
	}
	return os.Open(abs)
}

func (l *Local) Delete(_ int64, uri string) error {
	abs, err := l.resolve(uri)
	if err != nil {
		return err
	}
	if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (l *Local) Ingest(string, int64, string) error { return nil }

// ---- s3 ----

// S3Config S3 兼容对象存储配置（Aliyun OSS S3 端点 / AWS / MinIO 通用）。
type S3Config struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	Region    string
	Prefix    string // 对象键前缀（如 testpilot/artifacts；空=裸键）
	UseSSL    bool
	PathStyle bool // true=path-style（私有 MinIO）；false=virtual-hosted（AWS/OSS S3 网关默认）
}

// S3 对象存储后端。Aliyun OSS S3 网关要求 virtual-hosted 寻址（PathStyle=false），
// 私有 MinIO 部署通常需要 path-style。
type S3 struct {
	client *minio.Client
	bucket string
	prefix string
}

func NewS3(cfg S3Config) (*S3, error) {
	if cfg.Endpoint == "" || cfg.Bucket == "" || cfg.AccessKey == "" {
		return nil, fmt.Errorf("s3 backend requires endpoint/bucket/access_key")
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("invalid s3_endpoint %q", cfg.Endpoint)
	}
	secure := cfg.UseSSL
	if u.Scheme != "" {
		secure = u.Scheme == "https"
	}
	lookup := minio.BucketLookupDNS
	if cfg.PathStyle {
		lookup = minio.BucketLookupPath
	}
	client, err := minio.New(u.Host, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure:       secure,
		Region:       cfg.Region,
		BucketLookup: lookup,
	})
	if err != nil {
		return nil, fmt.Errorf("s3 client init: %w", err)
	}
	prefix := strings.Trim(cfg.Prefix, "/")
	if prefix != "" {
		prefix += "/"
	}
	return &S3{client: client, bucket: cfg.Bucket, prefix: prefix}, nil
}

// key 对象键：{prefix}{tenantID}/{uri}（uri 已归一化，无穿越概念）。
func (s *S3) key(tenantID int64, uri string) string {
	return fmt.Sprintf("%s%d/%s", s.prefix, tenantID, strings.TrimPrefix(filepath.ToSlash(filepath.Clean("/"+uri)), "/"))
}

func (s *S3) Open(tenantID int64, uri string) (io.ReadCloser, error) {
	key := s.key(tenantID, uri)
	// GetObject 惰性（404 在首次 Read 才暴露）；先 Stat 以便调用方及时得到缺失错误。
	if _, err := s.client.StatObject(context.Background(), s.bucket, key, minio.StatObjectOptions{}); err != nil {
		return nil, err
	}
	obj, err := s.client.GetObject(context.Background(), s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	return obj, nil
}

func (s *S3) Delete(tenantID int64, uri string) error {
	return s.client.RemoveObject(context.Background(), s.bucket, s.key(tenantID, uri),
		minio.RemoveObjectOptions{})
}

func (s *S3) Ingest(localPath string, tenantID int64, uri string) error {
	if _, err := s.client.FPutObject(context.Background(), s.bucket, s.key(tenantID, uri),
		localPath, minio.PutObjectOptions{}); err != nil {
		return err
	}
	if err := os.Remove(localPath); err != nil && !os.IsNotExist(err) {
		logging.L.Warnw("s3 ingest: remove local artifact failed", "path", localPath, "err", err)
	}
	return nil
}
