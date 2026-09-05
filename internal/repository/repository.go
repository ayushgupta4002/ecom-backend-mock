// Package repository implements all persistence and, critically, the
// transactional/locking strategy that enforces the service's invariants
// (no overselling, exactly-once checkout, exactly-once coupon redemption).
//
// GORM is used as the data-access layer, but every lock is taken
// explicitly via clause.Locking so that the concurrency behavior is
// visible at the call site rather than implied. Row locks are the
// mechanism that enforces the invariants; see DECISIONS.md.
package repository

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repo struct {
	db *gorm.DB
	// Reward program parameters, from the environment. They are deployment
	// configuration, not state, so they are never persisted: only what a
	// user has *earned* (their order count, their coupons) lives in the
	// database. Checkout never reads them -- a coupon carries the discount
	// it was minted with.
	milestoneEveryN int
	discountPercent int
}

func New(db *gorm.DB, milestoneEveryN, discountPercent int) *Repo {
	return &Repo{db: db, milestoneEveryN: milestoneEveryN, discountPercent: discountPercent}
}

// forUpdate is shorthand for `SELECT ... FOR UPDATE`, which takes an
// exclusive row lock held until the surrounding transaction commits or
// rolls back. Any other transaction that tries to lock the same row blocks
// until then -- this is what serializes competing checkouts.
func forUpdate(tx *gorm.DB) *gorm.DB {
	return tx.Clauses(clause.Locking{Strength: "UPDATE"})
}
