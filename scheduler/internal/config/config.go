// Package config Scheduler 运行配置：三级覆盖 —— 显式命令行参数 > 环境变量 > YAML > 内置默认。
//
// YAML 路径解析：--config > TP_CONFIG > ./scheduler.yaml（存在才加载）。
// 约定：yaml 键 = snake_case；环境变量 = "TP_" + 大写 yaml 键；flag = kebab-case。
// 三者由结构体 tag 自动推导，新增字段只需加结构体成员与默认值。
package config

import (
	"flag"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config 是 Scheduler 的运行配置。
type Config struct {
	HTTPAddr  string `yaml:"http_addr"`  // REST/前端托管监听
	GRPCAddr  string `yaml:"grpc_addr"`  // gRPC server（Worker/Copilot）
	StaticDir string `yaml:"static_dir"` // 前端 dist 托管目录（空=不托管）

	DBPath               string `yaml:"db_path"`                  // SQLite 文件（db_dsn 为空时使用）
	DBDSN                string `yaml:"db_dsn"`                   // PostgreSQL DSN（非空优先于 SQLite）
	DBMaxOpenConns       int    `yaml:"db_max_open_conns"`        // 连接池上限（0=驱动默认）
	DBMaxIdleConns       int    `yaml:"db_max_idle_conns"`        // 空闲连接上限（0=驱动默认）
	DBConnMaxLifetimeMin int    `yaml:"db_conn_max_lifetime_min"` // 连接最大存活分钟（0=不限）

	JWTSecret      string `yaml:"jwt_secret"`       // HS256 密钥（生产必改）
	JWTExpireHours int    `yaml:"jwt_expire_hours"` // token 有效期

	LogLevel  string `yaml:"log_level"`  // debug/info/warn/error
	LogFormat string `yaml:"log_format"` // text（默认彩色行）| json

	ArtifactDir          string `yaml:"artifact_dir"`           // 产物根目录（与 Worker 一致）
	RetentionDays        int    `yaml:"retention_run_days"`     // 运行数据保留天数（0=永久）
	RetentionIntervalMin int    `yaml:"retention_interval_min"` // 保留清理周期分钟

	BodyLimitMB     int `yaml:"body_limit_mb"`    // 请求体上限（OpenAPI 导入可超 4MB）
	ReadTimeoutSec  int `yaml:"read_timeout_sec"` // 0=不限
	WriteTimeoutSec int `yaml:"write_timeout_sec"`
	IdleTimeoutSec  int `yaml:"idle_timeout_sec"`

	OTelExporter string `yaml:"otel_exporter"` // "" 关闭 | stdout | otlp
	OTelEndpoint string `yaml:"otel_endpoint"` // otlp gRPC 地址

	DefaultTenantID int64 `yaml:"-"` // 存根租户（不开放配置）
}

// Defaults 内置默认（本地单二进制开发形态）。
func Defaults() Config {
	return Config{
		HTTPAddr:             ":8080",
		GRPCAddr:             ":9090",
		DBPath:               "testpilot.db",
		JWTSecret:            "dev-secret-change-me",
		JWTExpireHours:       24,
		LogLevel:             "info",
		LogFormat:            "text",
		ArtifactDir:          ".data/artifacts",
		RetentionIntervalMin: 60,
		BodyLimitMB:          64,
		OTelEndpoint:         "127.0.0.1:4317",
		DefaultTenantID:      1,
	}
}

// Load 解析 os.Args[1:] 与真实环境；错误时打印用法并以 2 退出。
func Load() Config {
	cfg, err := Resolve(os.Args[1:], os.LookupEnv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config: "+err.Error())
		os.Exit(2)
	}
	return cfg
}

// Resolve 按 默认 → YAML → 环境变量 → 显式 flag 合成最终配置（可测主逻辑）。
// getenv 形如 os.LookupEnv；args 为命令行（不含程序名）。
func Resolve(args []string, getenv func(string) (string, bool)) (Config, error) {
	cfg := Defaults()

	// 1) YAML（路径：--config/-config > TP_CONFIG > ./scheduler.yaml）
	if path := configPath(args, getenv); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return cfg, fmt.Errorf("read config %s: %w", path, err)
		}
		if err := yaml.Unmarshal(raw, &cfg); err != nil {
			return cfg, fmt.Errorf("parse config %s: %w", path, err)
		}
	}

	// 2) 环境变量覆盖（键由 yaml tag 推导）
	if err := applyEnv(&cfg, getenv); err != nil {
		return cfg, err
	}

	// 3) 显式 flag 覆盖（默认值预填当前 cfg，Parse 只改写出现的项）
	fs := flag.NewFlagSet("scheduler", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.String("config", "", "YAML 配置文件路径")
	bindFlags(fs, &cfg)
	if err := fs.Parse(args); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// configPath 找 YAML 路径；显式指定的文件必须存在，默认路径不存在则跳过。
func configPath(args []string, getenv func(string) (string, bool)) string {
	for i, a := range args {
		if a == "--config" || a == "-config" {
			if i+1 < len(args) {
				return args[i+1]
			}
		}
		if strings.HasPrefix(a, "--config=") || strings.HasPrefix(a, "-config=") {
			return strings.SplitN(a, "=", 2)[1]
		}
	}
	if v, ok := getenv("TP_CONFIG"); ok && v != "" {
		return v
	}
	if _, err := os.Stat("scheduler.yaml"); err == nil {
		return "scheduler.yaml"
	}
	return ""
}

// applyEnv 逐字段查 TP_<SCREAMING_SNAKE(yaml 键)>。
func applyEnv(cfg *Config, getenv func(string) (string, bool)) error {
	v := reflect.ValueOf(cfg).Elem()
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		key := yamlKey(t.Field(i))
		if key == "" {
			continue
		}
		raw, ok := getenv("TP_" + strings.ToUpper(key))
		if !ok || raw == "" {
			continue
		}
		if err := setFromString(v.Field(i), "TP_"+strings.ToUpper(key), raw); err != nil {
			return err
		}
	}
	return nil
}

// bindFlags 把每个可配字段绑定为 --kebab-case flag，默认值取当前字段值。
func bindFlags(fs *flag.FlagSet, cfg *Config) {
	v := reflect.ValueOf(cfg).Elem()
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		key := yamlKey(t.Field(i))
		if key == "" {
			continue
		}
		name := strings.ReplaceAll(key, "_", "-")
		f := v.Field(i)
		switch p := f.Addr().Interface().(type) {
		case *string:
			fs.StringVar(p, name, *p, key)
		case *int:
			fs.IntVar(p, name, *p, key)
		case *int64:
			fs.Int64Var(p, name, *p, key)
		}
	}
}

func yamlKey(f reflect.StructField) string {
	tag := strings.Split(f.Tag.Get("yaml"), ",")[0]
	if tag == "" || tag == "-" {
		return ""
	}
	return tag
}

func setFromString(f reflect.Value, name, raw string) error {
	switch f.Kind() {
	case reflect.String:
		f.SetString(raw)
	case reflect.Int, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return fmt.Errorf("%s: %q 不是整数", name, raw)
		}
		f.SetInt(n)
	default:
		return fmt.Errorf("%s: 不支持的字段类型 %s", name, f.Kind())
	}
	return nil
}
