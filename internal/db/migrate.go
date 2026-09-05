// Package db opens the database connection and applies migrations/seed
// files. Migrations are plain .sql files applied in filename order; GORM's
// AutoMigrate is deliberately not used, because migrations/0001_init.sql is
// the source of truth for the schema (its CHECK/UNIQUE constraints are part
// of how the service's invariants are enforced).
package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Open connects to Postgres via GORM.
func Open(dsn string) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	return db, nil
}

// Ping verifies the connection is usable.
func Ping(ctx context.Context, db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get sql.DB: %w", err)
	}
	return sqlDB.PingContext(ctx)
}

// Migrate applies any .sql files in dir not yet recorded in
// schema_migrations, each in its own transaction.
func Migrate(ctx context.Context, db *gorm.DB, dir string) error {
	err := db.WithContext(ctx).Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			filename    TEXT PRIMARY KEY,
			applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
		)`).Error
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	names, err := sqlFileNames(dir)
	if err != nil {
		return err
	}

	for _, name := range names {
		var count int64
		if err := db.WithContext(ctx).
			Table("schema_migrations").
			Where("filename = ?", name).
			Count(&count).Error; err != nil {
			return fmt.Errorf("check migration %s: %w", name, err)
		}
		if count > 0 {
			continue
		}

		content, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}

		err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec(string(content)).Error; err != nil {
				return fmt.Errorf("apply migration %s: %w", name, err)
			}
			return tx.Exec(`INSERT INTO schema_migrations (filename) VALUES (?)`, name).Error
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// Seed loads seed data from dir. Seed statements are idempotent
// (ON CONFLICT DO NOTHING), so this is safe on every startup.
func Seed(ctx context.Context, db *gorm.DB, dir string) error {
	names, err := sqlFileNames(dir)
	if err != nil {
		return err
	}
	for _, name := range names {
		content, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("read seed %s: %w", name, err)
		}
		if err := db.WithContext(ctx).Exec(string(content)).Error; err != nil {
			return fmt.Errorf("apply seed %s: %w", name, err)
		}
	}
	return nil
}

func sqlFileNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read dir %s: %w", dir, err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}
