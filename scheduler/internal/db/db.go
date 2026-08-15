package db

import (
	"fmt"
	"os"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/testpilot/testpilot/internal/model"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Pool 连接池参数（0=保持驱动默认；SQLite 一般无需设置）。
type Pool struct {
	MaxOpenConns       int
	MaxIdleConns       int
	ConnMaxLifetimeMin int
}

// Open 打开数据库并自动迁移 + 种子默认租户与 admin 账号。
// dsn 非空 → PostgreSQL（生产）；否则 SQLite（dev 默认）。
func Open(path, dsn string, pool Pool) (*gorm.DB, error) {
	cfg := &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Warn)}
	var d *gorm.DB
	var err error
	if dsn != "" {
		d, err = gorm.Open(postgres.Open(dsn), cfg)
	} else {
		// WAL：读写不互斥；busy_timeout：并发写等锁而非直接 SQLITE_BUSY；
		// _txlock=immediate：所有事务立即取写锁 → 事务内读取一致、配额检查串行化。
		// 注意：glebarez/go-sqlite 要求 '?' 位于 DSN 位置 >=1 才解析查询参数——
		// path 为空（SQLite 私有临时库，测试场景）时绝不能拼接，否则整个字符串
		// 会被当成数据库文件名（曾误创建 "?_pragma=…" 垃圾文件）。
		dsn := path
		if path != "" {
			dsn += "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate"
		}
		d, err = gorm.Open(sqlite.Open(dsn), cfg)
	}
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if pool.MaxOpenConns > 0 || pool.MaxIdleConns > 0 || pool.ConnMaxLifetimeMin > 0 {
		sqlDB, err := d.DB()
		if err != nil {
			return nil, fmt.Errorf("db handle: %w", err)
		}
		if pool.MaxOpenConns > 0 {
			sqlDB.SetMaxOpenConns(pool.MaxOpenConns)
		}
		if pool.MaxIdleConns > 0 {
			sqlDB.SetMaxIdleConns(pool.MaxIdleConns)
		}
		if pool.ConnMaxLifetimeMin > 0 {
			sqlDB.SetConnMaxLifetime(time.Duration(pool.ConnMaxLifetimeMin) * time.Minute)
		}
	}
	if err := d.AutoMigrate(model.AllModels()...); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if err := seed(d); err != nil {
		return nil, err
	}
	return d, nil
}

// seed 保证存在默认租户(id=1)与 admin 账号（owner）。
// 口令取 TP_ADMIN_PASSWORD（生产必须显式设置强口令），空则回落 admin123（仅本地开发）。
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
		adminPw := os.Getenv("TP_ADMIN_PASSWORD")
		if adminPw == "" {
			adminPw = "admin123"
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(adminPw), bcrypt.DefaultCost)
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
