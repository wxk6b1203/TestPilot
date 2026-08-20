package httpserver

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/testpilot/testpilot/internal/auth"
	"github.com/testpilot/testpilot/internal/config"
	"github.com/testpilot/testpilot/internal/model"
)

func TestAPITokenLifecycleAndAuthentication(t *testing.T) {
	cfg := config.Defaults()
	app, d := newTestApp(t, cfg)

	// 默认 seed 的 admin（owner）作为 token 颁发者。
	var user model.User
	if err := d.Where("username = ?", "admin").First(&user).Error; err != nil {
		t.Fatal(err)
	}
	jwt, err := auth.IssueToken(cfg.JWTSecret, user.ID, 1, auth.RoleOwner, 1)
	if err != nil {
		t.Fatal(err)
	}

	// 创建
	code, out := postJSON(t, app, "/api/v1/api-tokens", jwt,
		`{"name":"ci-token","scopes":["runs:read","runs:trigger"]}`)
	if code != 200 {
		t.Fatalf("create token: %d %v", code, out)
	}
	raw, _ := out["token"].(string)
	if !strings.HasPrefix(raw, "tp_") || len(raw) != len("tp_")+64 {
		t.Fatalf("bad raw token: %q", raw)
	}
	tokenID, _ := out["id"].(string)

	// 列表不含原始 token
	code, out = getJSON(t, app, "/api/v1/api-tokens", jwt)
	if code != 200 {
		t.Fatalf("list tokens: %d %v", code, out)
	}
	items := out["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items=%d", len(items))
	}
	meta := items[0].(map[string]any)
	if meta["name"] != "ci-token" || meta["token"] != nil {
		t.Fatalf("meta=%v", meta)
	}
	scopeList, ok := meta["scopes"].([]any)
	if !ok || len(scopeList) != 2 || scopeList[0] != "runs:read" {
		t.Fatalf("scopes=%v", meta["scopes"])
	}

	// API token 认证：能读 /me
	req := httptest.NewRequest("GET", "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		buf := make([]byte, 512)
		n, _ := resp.Body.Read(buf)
		t.Fatalf("api token auth: %d %s", resp.StatusCode, buf[:n])
	}

	// 删除后立即失效
	code, _ = sendJSON(t, app, "DELETE", "/api/v1/api-tokens/"+tokenID, jwt, "")
	if code != 200 {
		t.Fatalf("delete token: %d", code)
	}
	req = httptest.NewRequest("GET", "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	resp2, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 401 {
		t.Fatalf("deleted token should be rejected, got %d", resp2.StatusCode)
	}
}

func TestAPITokenExpiryAndTenantIsolation(t *testing.T) {
	cfg := config.Defaults()
	app, d := newTestApp(t, cfg)
	var user model.User
	if err := d.Where("username = ?", "admin").First(&user).Error; err != nil {
		t.Fatal(err)
	}
	jwt, _ := auth.IssueToken(cfg.JWTSecret, user.ID, 1, auth.RoleOwner, 1)

	future := time.Now().Add(time.Hour).Format(time.RFC3339)
	code, out := postJSON(t, app, "/api/v1/api-tokens", jwt,
		fmt.Sprintf(`{"name":"expires","expires_at":%q}`, future))
	if code != 200 {
		t.Fatalf("create: %d %v", code, out)
	}
	raw := out["token"].(string)

	// 手动改成已过期 → 认证拒绝
	var tok model.ApiToken
	if err := d.Where("name = ?", "expires").First(&tok).Error; err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Minute)
	if err := d.Model(&tok).Update("expires_at", &past).Error; err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("expired token status=%d, want 401", resp.StatusCode)
	}

	// 其他租户的 admin 不能看到本租户 token
	tenant2JWT, _ := auth.IssueToken(cfg.JWTSecret, user.ID, 2, auth.RoleOwner, 1)
	code, out = getJSON(t, app, "/api/v1/api-tokens", tenant2JWT)
	if code != 200 || len(out["items"].([]any)) != 0 {
		t.Fatalf("tenant isolation: code=%d items=%v", code, out["items"])
	}
}
