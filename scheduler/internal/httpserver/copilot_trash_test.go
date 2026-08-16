package httpserver

import (
	"fmt"
	"testing"
	"time"

	"github.com/testpilot/testpilot/internal/auth"
	"github.com/testpilot/testpilot/internal/config"
	"github.com/testpilot/testpilot/internal/model"
)

// TestCopilotTrashFlow 覆盖：软删除进回收站 → 活跃列表不再显示 → 回收站列表 → 手动彻底删除。
func TestCopilotTrashFlow(t *testing.T) {
	cfg := config.Defaults()
	cfg.JWTSecret = "test-secret-0123456789abcdef"
	app, d := newTestApp(t, cfg)
	tok := tokenFor(t, d, 1, 1, auth.RoleMember)

	sess := &model.CopilotSession{ID: model.NextID(), TenantID: 1, UserID: 1, Title: "hello"}
	if err := d.Create(sess).Error; err != nil {
		t.Fatal(err)
	}
	d.Create(&model.CopilotMessage{ID: model.NextID(), TenantID: 1, SessionID: sess.ID, Role: 1, Content: "hi"})

	// 软删除
	code, out := sendJSON(t, app, "DELETE", "/api/v1/copilot/sessions/"+fmt.Sprint(sess.ID), tok, "")
	if code != 200 {
		t.Fatalf("delete session: %d %v", code, out)
	}
	var dbSess model.CopilotSession
	if err := d.First(&dbSess, sess.ID).Error; err != nil {
		t.Fatal(err)
	}
	if dbSess.DeletedAt == nil {
		t.Fatal("deleted_at should be set after soft delete")
	}

	// 活跃列表不再出现
	code, out = getJSON(t, app, "/api/v1/copilot/sessions", tok)
	if code != 200 {
		t.Fatalf("list sessions: %d %v", code, out)
	}
	if len(out["items"].([]any)) != 0 {
		t.Fatalf("active list should be empty, got %v", out["items"])
	}

	// 回收站列表包含该会话和消息数
	code, out = getJSON(t, app, "/api/v1/copilot/trash", tok)
	if code != 200 {
		t.Fatalf("list trash: %d %v", code, out)
	}
	items := out["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("trash items = %d, want 1", len(items))
	}
	first := items[0].(map[string]any)
	if first["title"] != "hello" || first["message_count"] != float64(1) {
		t.Fatalf("unexpected trash item: %v", first)
	}

	// 手动彻底删除
	code, out = sendJSON(t, app, "DELETE", "/api/v1/copilot/trash/"+fmt.Sprint(sess.ID), tok, "")
	if code != 200 {
		t.Fatalf("purge trash: %d %v", code, out)
	}
	var count int64
	d.Model(&model.CopilotSession{}).Where("id = ?", sess.ID).Count(&count)
	if count != 0 {
		t.Fatal("session should be hard deleted")
	}
	d.Model(&model.CopilotMessage{}).Where("session_id = ?", sess.ID).Count(&count)
	if count != 0 {
		t.Fatal("messages should be hard deleted")
	}
}

// TestCopilotTrashIsolation 其他用户不能删除/查看不属于自己的回收站会话。
func TestCopilotTrashIsolation(t *testing.T) {
	cfg := config.Defaults()
	cfg.JWTSecret = "test-secret-0123456789abcdef"
	app, d := newTestApp(t, cfg)
	tok2 := tokenFor(t, d, 2, 1, auth.RoleMember)

	sess := &model.CopilotSession{ID: model.NextID(), TenantID: 1, UserID: 1, Title: "private"}
	if err := d.Create(sess).Error; err != nil {
		t.Fatal(err)
	}
	// 直接软删除，模拟已进入回收站
	deletedAt := time.Now()
	if err := d.Model(&model.CopilotSession{}).Where("id = ?", sess.ID).
		Update("deleted_at", deletedAt).Error; err != nil {
		t.Fatal(err)
	}

	// 用户 2 看不到
	code, out := getJSON(t, app, "/api/v1/copilot/trash", tok2)
	if code != 200 {
		t.Fatalf("list trash: %d %v", code, out)
	}
	if len(out["items"].([]any)) != 0 {
		t.Fatalf("user2 should see empty trash, got %v", out["items"])
	}

	// 用户 2 不能彻底删除
	code, _ = sendJSON(t, app, "DELETE", "/api/v1/copilot/trash/"+fmt.Sprint(sess.ID), tok2, "")
	if code != 404 {
		t.Fatalf("user2 purge should 404, got %d", code)
	}
}
