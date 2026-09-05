package repository

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"

	"github.com/ayush/checkout-rewards/internal/domain"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

// Coupons are minted automatically when a payment completes a milestone --
// see mintMilestoneCoupon in payments.go. There is deliberately no manual
// generation endpoint; see DECISIONS.md.

// ListCouponsForUser returns all coupons a user has earned.
func (r *Repo) ListCouponsForUser(ctx context.Context, userID int64) ([]domain.Coupon, error) {
	var rows []Coupon
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("milestone_number").
		Find(&rows).Error
	if err != nil {
		return nil, domain.WrapError(domain.CodeInternal, "list coupons", err)
	}

	out := make([]domain.Coupon, 0, len(rows))
	for _, c := range rows {
		out = append(out, toDomainCoupon(c))
	}
	return out, nil
}

func (r *Repo) GetCouponByCode(ctx context.Context, code string) (domain.Coupon, error) {
	var coupon Coupon
	err := r.db.WithContext(ctx).Where("code = ?", code).First(&coupon).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.Coupon{}, domain.NewError(domain.CodeNotFound, "coupon not found")
	}
	if err != nil {
		return domain.Coupon{}, domain.WrapError(domain.CodeInternal, "get coupon", err)
	}
	return toDomainCoupon(coupon), nil
}

func toDomainCoupon(c Coupon) domain.Coupon {
	return domain.Coupon{
		ID:              c.ID,
		UserID:          c.UserID,
		Global:          c.UserID == nil,
		Source:          domain.CouponSource(c.Source),
		Code:            c.Code,
		MilestoneNumber: c.MilestoneNumber,
		DiscountPercent: c.DiscountPercent,
		Status:          domain.CouponStatus(c.Status),
		OrderID:         c.OrderID,
		CreatedAt:       c.CreatedAt,
		RedeemedAt:      c.RedeemedAt,
	}
}

func generateCouponCode(milestone int64) (string, error) {
	return newCouponCode(fmt.Sprintf("REWARD-M%d", milestone))
}

func newCouponCode(prefix string) (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%s", prefix, strings.ToUpper(fmt.Sprintf("%x", b))), nil
}

// AdminCouponRequest describes a coupon an administrator wants to create.
// A nil UserID makes it global — redeemable by any customer. A nil
// DiscountPercent or empty Code falls back to the configured discount and a
// generated code.
type AdminCouponRequest struct {
	UserID          *int64
	DiscountPercent *int
	Code            string
}

// CreateAdminCoupon issues a coupon outside the milestone mechanism: a
// goodwill grant to one customer, a backfill for milestones passed before
// automatic minting existed, or a global promotional code.
//
// It never carries a milestone number, so it cannot collide with, or be
// mistaken for, an earned reward.
func (r *Repo) CreateAdminCoupon(ctx context.Context, req AdminCouponRequest) (domain.Coupon, error) {
	percent := r.discountPercent
	if req.DiscountPercent != nil {
		percent = *req.DiscountPercent
	}
	if percent < 0 || percent > 100 {
		return domain.Coupon{}, domain.NewError(domain.CodeValidation,
			"discount_percent must be between 0 and 100")
	}

	if req.UserID != nil {
		var exists int64
		if err := r.db.WithContext(ctx).Model(&User{}).Where("id = ?", *req.UserID).
			Count(&exists).Error; err != nil {
			return domain.Coupon{}, domain.WrapError(domain.CodeInternal, "check user", err)
		}
		if exists == 0 {
			return domain.Coupon{}, domain.NewError(domain.CodeNotFound, "user not found")
		}
	}

	code := strings.ToUpper(strings.TrimSpace(req.Code))
	if code == "" {
		generated, err := newCouponCode("PROMO")
		if err != nil {
			return domain.Coupon{}, domain.WrapError(domain.CodeInternal, "generate coupon code", err)
		}
		code = generated
	}

	coupon := Coupon{
		UserID:          req.UserID,
		Code:            code,
		Source:          string(domain.CouponFromAdmin),
		DiscountPercent: percent,
		Status:          string(domain.CouponAvailable),
	}
	// The UNIQUE constraint on code is the real guard against a duplicate;
	// checking first would be a race.
	if err := r.db.WithContext(ctx).Create(&coupon).Error; err != nil {
		if isUniqueViolation(err) {
			return domain.Coupon{}, domain.NewError(domain.CodeConflict,
				fmt.Sprintf("coupon code %q already exists", code))
		}
		return domain.Coupon{}, domain.WrapError(domain.CodeInternal, "create coupon", err)
	}
	return toDomainCoupon(coupon), nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
