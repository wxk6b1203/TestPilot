package db

import (
	"path/filepath"
	"testing"

	"github.com/testpilot/testpilot/internal/model"
	"golang.org/x/crypto/bcrypt"
)

func TestOpenSQLiteSeeds(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "test.db"), "", Pool{})
	if err != nil {
		t.Fatal(err)
	}
	// 默认租户
	var tenant model.Tenant
	if err := d.First(&tenant, 1).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if tenant.Name != "Default" || tenant.Status != 1 {
		t.Fatalf("tenant=%+v", tenant)
	}
	// admin 用户
	var admin model.User
	if err := d.Where("username = ?", "admin").First(&admin).Error; err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	if admin.Email != "admin@testpilot.local" || admin.Status != 1 || admin.DisplayName != "Admin" {
		t.Fatalf("admin=%+v", admin)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte("admin123")); err != nil {
		t.Fatalf("admin password verify: %v", err)
	}
	// owner 成员
	var m model.TenantMember
	if err := d.Where("tenant_id = ? AND user_id = ?", 1, admin.ID).First(&m).Error; err != nil {
		t.Fatalf("seed member: %v", err)
	}
	if m.Role != 1 {
		t.Fatalf("member role=%d", m.Role)
	}
}

func TestOpenIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "test.db")
	if _, err := Open(p, "", Pool{}); err != nil {
		t.Fatal(err)
	}
	// 重复 Open 同一路径：迁移 + 种子幂等
	d, err := Open(p, "", Pool{})
	if err != nil {
		t.Fatal(err)
	}
	var cnt int64
	d.Model(&model.Tenant{}).Where("id = 1").Count(&cnt)
	if cnt != 1 {
		t.Fatalf("tenant count=%d", cnt)
	}
	d.Model(&model.User{}).Where("username = ?", "admin").Count(&cnt)
	if cnt != 1 {
		t.Fatalf("admin count=%d", cnt)
	}
	d.Model(&model.TenantMember{}).Count(&cnt)
	if cnt != 1 {
		t.Fatalf("member count=%d", cnt)
	}
}

func TestPoolParams(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "test.db"), "",
		Pool{MaxOpenConns: 7, MaxIdleConns: 3, ConnMaxLifetimeMin: 1})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := d.DB()
	if err != nil {
		t.Fatal(err)
	}
	if got := sqlDB.Stats().MaxOpenConnections; got != 7 {
		t.Fatalf("MaxOpenConnections=%d", got)
	}
}

func TestPoolZeroValue(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "test.db"), "", Pool{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := d.DB()
	if err != nil {
		t.Fatal(err)
	}
	// 零值 Pool 不调用 Set*，database/sql 默认 0 = 不限
	if got := sqlDB.Stats().MaxOpenConnections; got != 0 {
		t.Fatalf("MaxOpenConnections=%d, want 0(unlimited)", got)
	}
}

func TestOpenPostgresBadDSN(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("should return error not panic: %v", r)
		}
	}()
	// 127.0.0.1:1 回环闭环端口，离线可用；连接被拒 → gorm ping/migrate 报错
	_, err := Open("", "postgres://testpilot:testpilot@127.0.0.1:1/testpilot?sslmode=disable&connect_timeout=1", Pool{})
	if err == nil {
		t.Fatal("expected error for unreachable postgres")
	}
}

func TestOpenEmptyPathAndDSN(t *testing.T) {
	// path 与 dsn 同时为空：走 SQLite 分支，空文件名 = SQLite 私有临时库，
	// 按实现应正常打开并完成迁移 + 种子。
	d, err := Open("", "", Pool{})
	if err != nil {
		t.Fatalf("Open with empty path/dsn: %v", err)
	}
	var cnt int64
	d.Model(&model.Tenant{}).Where("id = 1").Count(&cnt)
	if cnt != 1 {
		t.Fatalf("tenant count=%d", cnt)
	}
}
