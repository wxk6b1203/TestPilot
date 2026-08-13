package db

import (
	"fmt"

	"github.com/glebarez/sqlite"
	"github.com/testpilot/testpilot/internal/model"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Open 打开数据库并自动迁移 + 种子默认租户与 admin 账号。
// dsn 非空 → PostgreSQL（生产）；否则 SQLite（dev 默认）。
func Open(path, dsn string) (*gorm.DB, error) {
	cfg := &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Warn)}
	var d *gorm.DB
	var err error
	if dsn != "" {
		d, err = gorm.Open(postgres.Open(dsn), cfg)
	} else {
		d, err = gorm.Open(sqlite.Open(path), cfg)
	}
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := d.AutoMigrate(model.AllModels()...); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if err := seed(d); err != nil {
		return nil, err
	}
	return d, nil
}

// seed 保证存在默认租户(id=1)与 admin/admin123（owner）。
func seed(d *gorm.DB) error {
	var cnt int64
	d.Model(&model.Tenant{}).Where("id = 1").Count(&cnt)
	if cnt == 0 {
		if err := d.Create(&model.Tenant{ID: 1, Name: "Default", Status: 1}).Error; err != nil {
			return fmt.Errorf("seed tenant: %w", err)
		}
	}
	d.Model(&model.User{}).Where("username = ?", "admin").Count(&cnt)
	if cnt == 0 {
		hash, err := bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		u := &model.User{ID: model.NextID(), Username: "admin", Email: "admin@testpilot.local",
			PasswordHash: string(hash), DisplayName: "Admin", Status: 1}
		if err := d.Create(u).Error; err != nil {
			return fmt.Errorf("seed admin: %w", err)
		}
		m := &model.TenantMember{ID: model.NextID(), TenantID: 1, UserID: u.ID, Role: 1} // 1=owner
		if err := d.Create(m).Error; err != nil {
			return fmt.Errorf("seed member: %w", err)
		}
	}
	return nil
}
