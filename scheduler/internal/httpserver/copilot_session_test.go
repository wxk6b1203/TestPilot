package httpserver

import (
	"fmt"
	"testing"

	"github.com/testpilot/testpilot/internal/auth"
	"github.com/testpilot/testpilot/internal/config"
	"github.com/testpilot/testpilot/internal/model"
)

// TestCopilotSessionRename 覆盖：重命名生效 → 其他用户不可改 → 空标题 400 → 不存在 404。
func TestCopilotSessionRename(t *testing.T) {
	cfg := config.Defaults()
	cfg.JWTSecret = "test-secret-0123456789abcdef"
	app, d := newTestApp(t, cfg)
	tok1 := tokenFor(t, d, 1, 1, auth.RoleMember)
	tok2 := tokenFor(t, d, 2, 1, auth.RoleMember)

	sess := &model.CopilotSession{ID: model.NextID(), TenantID: 1, UserID: 1, Title: "旧标题"}
	if err := d.Create(sess).Error; err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/copilot/sessions/" + fmt.Sprint(sess.ID)

	// 本人重命名成功
	code, out := sendJSON(t, app, "PUT", path, tok1, `{"title":"新标题"}`)
	if code != 200 {
		t.Fatalf("rename session: %d %v", code, out)
	}
	var got model.CopilotSession
	if err := d.First(&got, sess.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Title != "新标题" {
		t.Fatalf("title = %q, want 新标题", got.Title)
	}

	// 其他用户不可改（404 隔离，不泄露存在性）
	if code, _ := sendJSON(t, app, "PUT", path, tok2, `{"title":"hacked"}`); code != 404 {
		t.Fatalf("user2 rename should 404, got %d", code)
	}

	// 空标题 400
	if code, _ := sendJSON(t, app, "PUT", path, tok1, `{"title":"   "}`); code != 400 {
		t.Fatalf("empty title should 400, got %d", code)
	}

	// 不存在的会话 404
	missing := "/api/v1/copilot/sessions/" + fmt.Sprint(model.NextID())
	if code, _ := sendJSON(t, app, "PUT", missing, tok1, `{"title":"x"}`); code != 404 {
		t.Fatalf("missing session should 404, got %d", code)
	}

	// 改名为同名（MySQL 同值 UPDATE 受影响行数为 0）：应 200 而非 404
	if code, out := sendJSON(t, app, "PUT", path, tok1, `{"title":"新标题"}`); code != 200 {
		t.Fatalf("same-title rename should 200, got %d %v", code, out)
	}
}
