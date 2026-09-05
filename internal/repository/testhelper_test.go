package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/ayush/checkout-rewards/internal/config"
	"github.com/ayush/checkout-rewards/internal/db"
	"github.com/ayush/checkout-rewards/internal/domain"
	"github.com/ayush/checkout-rewards/internal/repository"
	"gorm.io/gorm"
)

// Reward parameters used by the tests. Set explicitly rather than read from
// the environment so expectations stay deterministic whatever .env contains.
const (
	testMilestoneEveryN = 5
	testDiscountPercent = 10
)

// Seeded user ids (see seed/seed.sql).
const (
	aaravID int64 = 1
	priyaID int64 = 2
	rohanID int64 = 3
)

// setupRepo connects, migrates, truncates every table, then loads seed data
// and a known reward configuration.
//
// NOTE: these tests share the service's database and TRUNCATE it, so
// DATABASE_URL must point at a development database.
func setupRepo(t *testing.T) (*repository.Repo, *gorm.DB) {
	t.Helper()

	// Falls back to the repo-root .env so `go test ./...` works with no
	// setup beyond `make up`.
	cfg, err := config.Load("../../.env")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	gormDB, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := db.Ping(ctx, gormDB); err != nil {
		t.Fatalf("cannot reach database (%v). Is Postgres running? Try `make up`.", err)
	}

	if err := db.Migrate(ctx, gormDB, "../../migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	err = gormDB.WithContext(ctx).Exec(`
		TRUNCATE order_items, orders, coupons, cart_items, carts, products, users RESTART IDENTITY CASCADE
	`).Error
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}

	if err := db.Seed(ctx, gormDB, "../../seed"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return repository.New(gormDB, testMilestoneEveryN, testDiscountPercent), gormDB
}

// checkoutAndPay runs the full two-phase flow: checkout opens a pending
// order, then the provider webhook settles it successfully.
func checkoutAndPay(t *testing.T, repo *repository.Repo, cartID int64, couponCode *string) domain.Order {
	t.Helper()
	ctx := context.Background()

	pending, err := repo.Checkout(ctx, cartID, couponCode)
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if pending.Status != domain.OrderPending {
		t.Fatalf("expected pending order, got %s", pending.Status)
	}
	paid, err := repo.SettlePayment(ctx, pending.Payment.ProviderRef, true)
	if err != nil {
		t.Fatalf("settle payment: %v", err)
	}
	return paid
}
