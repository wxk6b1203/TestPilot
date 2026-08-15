package httpserver

import (
	"fmt"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/testpilot/testpilot/internal/auth"
	"github.com/testpilot/testpilot/internal/config"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

// TestCreateForcesTenantID 回归：P0 A5 跨租户创建漏洞。
// 此前 assignIDs 用"仅当字段为 0 时赋值"，请求体带 tenant_id 即可把资源
// 创建到任意租户；现在 createOf/assignIDs 强制覆盖 ID/TenantID。
func TestCreateForcesTenantID(t *testing.T) {
	cfg := config.Defaults()
	cfg.JWTSecret = "test-secret-0123456789abcdef"
	app, d := newTestApp(t, cfg)

	// 两个租户的用户
	tok1 := tokenFor(t, d, 1, 1, auth.RoleOwner)
	tok2 := tokenFor(t, d, 2, 2, auth.RoleOwner)

	// 租户 2 的用户尝试把 project 创建到租户 1（请求体带 tenant_id 注入）
	code, out := postJSON(t, app, "/api/v1/projects", tok2,
		`{"name":"sneaky","tenant_id":"1"}`)
	if code != 200 {
		t.Fatalf("create project: %d %v", code, out)
	}
	var p model.Project
	if err := d.Where("name = ?", "sneaky").First(&p).Error; err != nil {
		t.Fatal(err)
	}
	if p.TenantID != 2 {
		t.Fatalf("project tenant_id = %d, want 2（请求体 tenant_id 注入必须被忽略）", p.TenantID)
	}
	// id 同样不可被客户端指定
	if got := out["id"]; fmt.Sprint(got) != fmt.Sprint(p.ID) {
		t.Fatalf("response id %v != db id %d", got, p.ID)
	}

	// 租户 1 看不到该资源（隔离未被污染）
	code, out = getJSON(t, app, "/api/v1/projects/"+fmt.Sprint(p.ID), tok1)
	if code != 404 {
		t.Fatalf("cross-tenant read should 404, got %d", code)
	}
}

func tokenFor(t *testing.T, d *gorm.DB, userID, tenantID int64, role int16) string {
	t.Helper()
	tok, err := auth.IssueToken("test-secret-0123456789abcdef", userID, tenantID, role, 1)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func getJSON(t *testing.T, app *fiber.App, path, token string) (int, map[string]any) {
	t.Helper()
	return sendJSON(t, app, fiber.MethodGet, path, token, "")
}

