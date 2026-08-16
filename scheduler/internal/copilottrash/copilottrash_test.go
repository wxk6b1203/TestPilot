package copilottrash

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/testpilot/testpilot/internal/db"
	"github.com/testpilot/testpilot/internal/model"
)

// TestCleanup 删除超过保留期的软删除会话及其消息，保留近期会话和未删除会话。
func TestCleanup(t *testing.T) {
	tmp := t.TempDir()
	d, err := db.Open(filepath.Join(tmp, "test.db"), "", db.Pool{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	old := now.AddDate(0, 0, -40)
	recent := now.AddDate(0, 0, -5)

	mk := func(id int64, deletedAt *time.Time) {
		d.Create(&model.CopilotSession{ID: id, TenantID: 1, UserID: 1, Title: "s", DeletedAt: deletedAt})
		d.Create(&model.CopilotMessage{ID: id * 100, TenantID: 1, SessionID: id, Role: 1, Content: "hi"})
	}
	oldTime := old
	recentTime := recent
	mk(1001, &oldTime)
	mk(1002, &recentTime)
	d.Create(&model.CopilotSession{ID: 1003, TenantID: 1, UserID: 1, Title: "active"}) // 未删除

	n := Cleanup(d, 30)
	if n != 1 {
		t.Fatalf("expected 1 old session cleaned, got %d", n)
	}
	var count int64
	d.Model(&model.CopilotSession{}).Where("id = ?", 1001).Count(&count)
	if count != 0 {
		t.Error("old session should be gone")
	}
	d.Model(&model.CopilotMessage{}).Where("session_id = ?", 1001).Count(&count)
	if count != 0 {
		t.Error("old messages should be gone")
	}
	d.Model(&model.CopilotSession{}).Where("id = ?", 1002).Count(&count)
	if count != 1 {
		t.Error("recent trash session should remain")
	}
	d.Model(&model.CopilotSession{}).Where("id = ?", 1003).Count(&count)
	if count != 1 {
		t.Error("active session should remain")
	}
}

// TestCleanupDisabled days<=0 不清理。
func TestCleanupDisabled(t *testing.T) {
	tmp := t.TempDir()
	d, err := db.Open(filepath.Join(tmp, "test.db"), "", db.Pool{})
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().AddDate(0, 0, -40)
	d.Create(&model.CopilotSession{ID: 2001, TenantID: 1, UserID: 1, DeletedAt: &old})
	if n := Cleanup(d, 0); n != 0 {
		t.Fatalf("expected 0, got %d", n)
	}
	var count int64
	d.Model(&model.CopilotSession{}).Where("id = ?", 2001).Count(&count)
	if count != 1 {
		t.Error("disabled cleanup should not delete")
	}
}
