package httpserver

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/testpilot/testpilot/internal/config"
	"github.com/testpilot/testpilot/internal/db"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

// newTestApp 全量 App 测试底座（disp/run/cron 仅注册/设置路由用不到，传 nil）。
func newTestApp(t *testing.T, cfg config.Config) (*fiber.App, *gorm.DB) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"), "", db.Pool{})
	if err != nil {
		t.Fatal(err)
	}
	return New(d, cfg, nil, nil, nil, nil).App(), d
}

func postJSON(t *testing.T, app *fiber.App, path, token, body string) (int, map[string]any) {
	t.Helper()
	return sendJSON(t, app, fiber.MethodPost, path, token, body)
}

func sendJSON(t *testing.T, app *fiber.App, method, path, token, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	if token != "" {
		req.Header.Set(fiber.HeaderAuthorization, "Bearer "+token)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestRegisterDisabledByDefault(t *testing.T) {
	app, _ := newTestApp(t, config.Defaults())
	code, out := postJSON(t, app, "/api/v1/auth/register", "",
		`{"username":"newbie","password":"password123"}`)
	if code != 403 {
		t.Fatalf("want 403, got %d: %v", code, out)
	}
	e, _ := out["error"].(map[string]any)
	if e["code"] != "REGISTRATION_DISABLED" {
		t.Fatalf("code=%v", e)
	}
}

func TestRegisterAndLogin(t *testing.T) {
	cfg := config.Defaults()
	cfg.RegistrationEnabled = true
	app, d := newTestApp(t, cfg)

	code, out := postJSON(t, app, "/api/v1/auth/register", "",
		`{"username":"alice","password":"supersecret1","display_name":"Alice","tenant_name":"Alice 组"}`)
	if code != 200 {
		t.Fatalf("register: %d %v", code, out)
	}
	if out["token"] == nil || out["token"] == "" {
		t.Fatal("register should issue token")
	}
	if out["role"].(float64) != 1 { // owner
		t.Fatalf("role=%v, want owner(1)", out["role"])
	}

	// 新账号可登录（自建租户 owner）
	code, out = postJSON(t, app, "/api/v1/auth/login", "",
		`{"username":"alice","password":"supersecret1"}`)
	if code != 200 || out["token"] == nil {
		t.Fatalf("login after register: %d %v", code, out)
	}

	// 审计落库（注册不经 auth 中间件，手动落）
	var n int64
	d.Model(&model.AuditLog{}).Where("action = 'register' AND actor = 1").Count(&n)
	if n != 1 {
		t.Fatalf("register audit missing: %d", n)
	}

	// 重复用户名 → 409
	code, out = postJSON(t, app, "/api/v1/auth/register", "",
		`{"username":"alice","password":"supersecret1"}`)
	if code != 409 {
		t.Fatalf("duplicate: want 409, got %d %v", code, out)
	}
	e, _ := out["error"].(map[string]any)
	if e["code"] != "USERNAME_TAKEN" {
		t.Fatalf("code=%v", e)
	}
}

func TestRegisterValidation(t *testing.T) {
	cfg := config.Defaults()
	cfg.RegistrationEnabled = true
	app, _ := newTestApp(t, cfg)

	for _, body := range []string{
		`{"username":"ab","password":"password123"}`, // 用户名过短
		`{"username":"bob","password":"short"}`,      // 密码过短
	} {
		code, _ := postJSON(t, app, "/api/v1/auth/register", "", body)
		if code == 200 {
			t.Fatalf("body %q should fail validation", body)
		}
	}
}

func TestTenantSettingsCRUD(t *testing.T) {
	app, _ := newTestApp(t, config.Defaults())

	// 种子 admin/admin123 登录（admin 角色可治理租户设置）
	code, out := postJSON(t, app, "/api/v1/auth/login", "",
		`{"username":"admin","password":"admin123"}`)
	if code != 200 {
		t.Fatalf("admin login: %d %v", code, out)
	}
	token := out["token"].(string)

	// PUT 建 + 更新
	code, out = sendJSON(t, app, fiber.MethodPut, "/api/v1/tenant/settings/feature_beta", token, `{"value":"on"}`)
	if code != 200 || out["key"] != "feature_beta" || out["value"] != "on" {
		t.Fatalf("upsert: %d %v", code, out)
	}
	code, out = sendJSON(t, app, fiber.MethodPut, "/api/v1/tenant/settings/feature_beta", token, `{"value":"off"}`)
	if code != 200 || out["value"] != "off" {
		t.Fatalf("upsert update: %d %v", code, out)
	}

	// GET 列表
	req := httptest.NewRequest(fiber.MethodGet, "/api/v1/tenant/settings", nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer "+token)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Items []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"items"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if len(list.Items) != 1 || list.Items[0].Key != "feature_beta" || list.Items[0].Value != "off" {
		t.Fatalf("list: %+v", list.Items)
	}

	// DELETE + 再删 404（顺序固定，勿用 map 迭代）
	for _, want := range []int{200, 404} {
		req = httptest.NewRequest(fiber.MethodDelete, "/api/v1/tenant/settings/feature_beta", nil)
		req.Header.Set(fiber.HeaderAuthorization, "Bearer "+token)
		resp, err = app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("delete: want %d, got %d", want, resp.StatusCode)
		}
	}

	// 非法 key → 400（空格需 URL 编码，fiber 解码后由正则拒绝）
	code, _ = sendJSON(t, app, fiber.MethodPut, "/api/v1/tenant/settings/bad%20key!", token, `{"value":"x"}`)
	if code != 400 {
		t.Fatalf("invalid key: want 400, got %d", code)
	}
}
