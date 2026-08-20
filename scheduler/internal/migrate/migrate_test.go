package migrate

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

func openSQLite(t *testing.T) *gorm.DB {
	t.Helper()
	d, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func appliedVersions(t *testing.T, d *gorm.DB) []int {
	t.Helper()
	rows, err := d.Raw(`SELECT version FROM schema_migrations`).Rows()
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, int(v))
	}
	return out
}

func TestRunFreshDatabaseCreatesBaselineAndPendingMigrations(t *testing.T) {
	d := openSQLite(t)
	n, err := Run(d, model.AllModels())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 { // 仅 v2 api_tokens；v1 是基线
		t.Fatalf("executed=%d, want 1", n)
	}
	if !d.Migrator().HasTable("tenants") {
		t.Fatal("baseline automigrate did not create tenants")
	}
	if !d.Migrator().HasTable("api_tokens") {
		t.Fatal("migration did not create api_tokens")
	}
	if got := appliedVersions(t, d); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("applied versions = %v, want [1 2]", got)
	}

	// 幂等：再次运行不应重复执行或报错
	n, err = Run(d, model.AllModels())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("second run executed=%d, want 0", n)
	}
}

func TestRunExistingDatabaseGetsBaselineWithoutRerunningDDL(t *testing.T) {
	d := openSQLite(t)
	// 模拟存量库：已有 GORM 业务表，但没有 schema_migrations。
	if err := d.AutoMigrate(model.AllModels()...); err != nil {
		t.Fatal(err)
	}
	marker := model.Tenant{ID: model.NextID(), Name: "existing", Status: 1}
	if err := d.Create(&marker).Error; err != nil {
		t.Fatal(err)
	}

	n, err := Run(d, model.AllModels())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("executed=%d, want 1", n)
	}
	var cnt int64
	if err := d.Model(&model.Tenant{}).Count(&cnt).Error; err != nil {
		t.Fatal(err)
	}
	if cnt != 1 {
		t.Fatalf("tenant count=%d, want 1 (existing row preserved)", cnt)
	}
	if !d.Migrator().HasTable("api_tokens") {
		t.Fatal("api_tokens not created")
	}
}

func TestRunWithoutMigrationsTableAndWithoutModelsStillCreatesHistory(t *testing.T) {
	d := openSQLite(t)
	if _, err := Run(d, nil); err != nil {
		t.Fatal(err)
	}
	// 没有模型可建，也没有 tenants 表：基线仍应写入，后续迁移照常执行。
	if !d.Migrator().HasTable("schema_migrations") {
		t.Fatal("schema_migrations missing")
	}
	if !d.Migrator().HasTable("api_tokens") {
		t.Fatal("api_tokens missing")
	}
}

func TestMigrationAppliedTimestamp(t *testing.T) {
	d := openSQLite(t)
	if _, err := Run(d, model.AllModels()); err != nil {
		t.Fatal(err)
	}
	rows, err := d.Table("schema_migrations").Order("version asc").Rows()
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var version int64
		var name string
		var appliedAt time.Time
		if err := rows.Scan(&version, &name, &appliedAt); err != nil {
			t.Fatal(err)
		}
		if appliedAt.IsZero() {
			t.Fatalf("version %d has zero applied_at", version)
		}
	}
}
