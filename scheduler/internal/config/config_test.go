package config

import (
	"os"
	"path/filepath"
	"testing"
)

// envOf 构造只认给定键值的 LookupEnv。
func envOf(kv map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := kv[k]
		return v, ok
	}
}

func TestResolveDefaults(t *testing.T) {
	c, err := Resolve(nil, envOf(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTPAddr != ":8080" || c.GRPCAddr != ":9090" || c.JWTExpireHours != 24 ||
		c.BodyLimitMB != 64 || c.RetentionIntervalMin != 60 || c.CopilotTrashDays != 30 ||
		c.LogFormat != "text" {
		t.Fatalf("defaults wrong: %+v", c)
	}
}

func TestResolveYAML(t *testing.T) {
	f := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(f, []byte("http_addr: ':18088'\njwt_expire_hours: 48\nbody_limit_mb: 128\n"), 0o600)
	c, err := Resolve([]string{"--config", f}, envOf(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTPAddr != ":18088" || c.JWTExpireHours != 48 || c.BodyLimitMB != 128 {
		t.Fatalf("yaml not applied: %+v", c)
	}
	if c.GRPCAddr != ":9090" { // 未覆盖字段保持默认
		t.Fatalf("default lost: %+v", c)
	}
}

func TestResolvePrecedence(t *testing.T) {
	// flag > env > yaml > default：用四个不同字段各压一层，再用同字段三层混战。
	f := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(f, []byte("http_addr: ':20001'\nlog_level: warn\njwt_expire_hours: 48\nbody_limit_mb: 128\n"), 0o600)
	c, err := Resolve(
		[]string{"--config", f, "--http-addr", ":20003", "--log-level", "debug"},
		envOf(map[string]string{"TP_HTTP_ADDR": ":20002", "TP_JWT_EXPIRE_HOURS": "72"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTPAddr != ":20003" { // flag 赢 env 与 yaml
		t.Fatalf("flag should win: %s", c.HTTPAddr)
	}
	if c.JWTExpireHours != 72 { // env 赢 yaml(48)
		t.Fatalf("env should win over yaml: %d", c.JWTExpireHours)
	}
	if c.BodyLimitMB != 128 { // yaml 赢默认(64)
		t.Fatalf("yaml should win over default: %d", c.BodyLimitMB)
	}
	if c.LogLevel != "debug" {
		t.Fatalf("flag not applied: %s", c.LogLevel)
	}
}

func TestResolveEnvKeyDerivation(t *testing.T) {
	// yaml 键 snake_case → TP_ + 大写；flag kebab-case。
	c, err := Resolve(
		[]string{"--retention-run-days", "30"},
		envOf(map[string]string{
			"TP_RETENTION_RUN_DAYS":           "15",
			"TP_OTEL_EXPORTER":                "stdout",
			"TP_COPILOT_TRASH_RETENTION_DAYS": "15",
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if c.RetentionDays != 30 { // flag(30) > env(15)
		t.Fatalf("got %d", c.RetentionDays)
	}
	if c.OTelExporter != "stdout" {
		t.Fatalf("otel env not applied: %q", c.OTelExporter)
	}
	if c.CopilotTrashDays != 15 {
		t.Fatalf("copilot trash env not applied: %d", c.CopilotTrashDays)
	}
}

func TestResolveBadIntEnv(t *testing.T) {
	_, err := Resolve(nil, envOf(map[string]string{"TP_BODY_LIMIT_MB": "abc"}))
	if err == nil {
		t.Fatal("expect error on non-integer env")
	}
}

func TestResolveMissingYAML(t *testing.T) {
	_, err := Resolve([]string{"--config", filepath.Join(t.TempDir(), "nope.yaml")}, envOf(nil))
	if err == nil {
		t.Fatal("expect error on missing explicit config")
	}
}

func TestResolveBadFlag(t *testing.T) {
	if _, err := Resolve([]string{"--nope"}, envOf(nil)); err == nil {
		t.Fatal("expect error on unknown flag")
	}
}
