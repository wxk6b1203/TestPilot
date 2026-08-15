package httpserver

import (
	"testing"

	"github.com/testpilot/testpilot/internal/auth"
	"github.com/testpilot/testpilot/internal/config"
	"github.com/testpilot/testpilot/internal/model"
)

// TestAddMemberNewUser 回归：addMember 事务外重复 Create(m) 撞主键导致恒 500。
// 修复后新用户应 200，且 users/tenant_members 各恰好一行；重复添加返回 409 且不产生重复行。
func TestAddMemberNewUser(t *testing.T) {
	cfg := config.Defaults()
	cfg.JWTSecret = "test-secret-0123456789abcdef"
	app, d := newTestApp(t, cfg)
	tok := tokenFor(t, d, 1, 1, auth.RoleOwner)

	code, out := postJSON(t, app, "/api/v1/tenant/members", tok,
		`{"username":"newbie","role":2}`)
	if code != 200 {
		t.Fatalf("add new member: %d %v", code, out)
	}
	var users int64
	d.Model(&model.User{}).Where("username = ?", "newbie").Count(&users)
	if users != 1 {
		t.Fatalf("users rows = %d, want 1", users)
	}
	// 成员行按 user 过滤（seed 的 admin 也是成员，全表计数会干扰）
	var nu model.User
	if err := d.Where("username = ?", "newbie").First(&nu).Error; err != nil {
		t.Fatal(err)
	}
	var members int64
	d.Model(&model.TenantMember{}).Where("user_id = ?", nu.ID).Count(&members)
	if members != 1 {
		t.Fatalf("member rows = %d, want 1", members)
	}

	// 幂等：再次添加同用户 → 409，且不产生新行
	code, _ = postJSON(t, app, "/api/v1/tenant/members", tok, `{"username":"newbie","role":2}`)
	if code != 409 {
		t.Fatalf("duplicate add want 409, got %d", code)
	}
	var members2 int64
	d.Model(&model.TenantMember{}).Where("user_id = ?", nu.ID).Count(&members2)
	if members2 != 1 {
		t.Fatalf("after duplicate member rows = %d, want 1", members2)
	}
}

// TestAddMemberExistingUser 回归：既有用户被无条件重建（撞 username 唯一索引）导致恒 500。
// 修复后应复用既有用户 ID 只建成员。
func TestAddMemberExistingUser(t *testing.T) {
	cfg := config.Defaults()
	cfg.JWTSecret = "test-secret-0123456789abcdef"
	app, d := newTestApp(t, cfg)
	tok := tokenFor(t, d, 1, 1, auth.RoleOwner)

	u := &model.User{ID: model.NextID(), Username: "bob",
		PasswordHash: "x", DisplayName: "Bob", Status: 1}
	if err := d.Create(u).Error; err != nil {
		t.Fatal(err)
	}

	code, out := postJSON(t, app, "/api/v1/tenant/members", tok, `{"username":"bob","role":2}`)
	if code != 200 {
		t.Fatalf("add existing user: %d %v", code, out)
	}
	var members int64
	d.Model(&model.TenantMember{}).Where("user_id = ?", u.ID).Count(&members)
	if members != 1 {
		t.Fatalf("member rows for bob = %d, want 1", members)
	}
	// 用户未被重复创建
	var users int64
	d.Model(&model.User{}).Where("username = ?", "bob").Count(&users)
	if users != 1 {
		t.Fatalf("users rows = %d, want 1", users)
	}
}
