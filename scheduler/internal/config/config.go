package config

import "os"

// Config 是 Scheduler 的运行配置（环境变量覆盖，默认本地单二进制）。
type Config struct {
	HTTPAddr        string // REST/前端托管
	GRPCAddr        string // gRPC server（Worker/Copilot）
	DBPath          string // SQLite 文件路径（TP_DB_DSN 为空时使用）
	DBDSN           string // PostgreSQL DSN（postgres://... 或 key=value；非空则优先于 SQLite）
	JWTSecret       string
	LogLevel        string // debug/info/warn/error
	DefaultTenantID int64  // 存根租户（认证接入前的默认租户）
	RetentionDays   int    // 运行数据保留天数（0=永久）
	ArtifactDir     string // 产物根目录（与 Worker TP_ARTIFACT_DIR 一致）
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// Load 读取配置。
func Load() Config {
	return Config{
		HTTPAddr:        getenv("TP_HTTP_ADDR", ":8080"),
		GRPCAddr:        getenv("TP_GRPC_ADDR", ":9090"),
		DBPath:          getenv("TP_DB_PATH", "testpilot.db"),
		DBDSN:           os.Getenv("TP_DB_DSN"),
		JWTSecret:       getenv("TP_JWT_SECRET", "dev-secret-change-me"),
		LogLevel:        getenv("TP_LOG_LEVEL", "info"),
		DefaultTenantID: 1,
		RetentionDays:   atoi(getenv("TP_RETENTION_RUN_DAYS", "0")),
		ArtifactDir:     getenv("TP_ARTIFACT_DIR", ".data/artifacts"),
	}
}
