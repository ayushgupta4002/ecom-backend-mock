package repository

import (
	"context"
	"errors"

	"github.com/ayush/checkout-rewards/internal/domain"
	"gorm.io/gorm"
)

func (r *Repo) ListUsers(ctx context.Context) ([]domain.User, error) {
	var rows []User
	if err := r.db.WithContext(ctx).Order("name").Find(&rows).Error; err != nil {
		return nil, domain.WrapError(domain.CodeInternal, "list users", err)
	}

	out := make([]domain.User, 0, len(rows))
	for _, u := range rows {
		out = append(out, toDomainUser(u))
	}
	return out, nil
}

func (r *Repo) GetUser(ctx context.Context, id int64) (domain.User, error) {
	var u User
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.User{}, domain.NewError(domain.CodeNotFound, "user not found")
	}
	if err != nil {
		return domain.User{}, domain.WrapError(domain.CodeInternal, "get user", err)
	}
	return toDomainUser(u), nil
}

// GetRewardStatus reports how far a user is from their next coupon. Purely
// a read, so it is safe to poll.
func (r *Repo) GetRewardStatus(ctx context.Context, userID int64) (domain.RewardStatus, error) {
	db := r.db.WithContext(ctx)

	var u User
	err := db.Where("id = ?", userID).First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.RewardStatus{}, domain.NewError(domain.CodeNotFound, "user not found")
	}
	if err != nil {
		return domain.RewardStatus{}, domain.WrapError(domain.CodeInternal, "get user", err)
	}

	// Only milestone coupons are reward progress; admin grants are not.
	var generated int64
	if err := db.Model(&Coupon{}).
		Where("user_id = ? AND source = ?", userID, string(domain.CouponFromMilestone)).
		Count(&generated).Error; err != nil {
		return domain.RewardStatus{}, domain.WrapError(domain.CodeInternal, "count coupons", err)
	}

	n := int64(r.milestoneEveryN)
	reached := u.SuccessfulOrderCount / n

	return domain.RewardStatus{
		UserID:               userID,
		SuccessfulOrderCount: u.SuccessfulOrderCount,
		MilestoneEveryN:      r.milestoneEveryN,
		DiscountPercent:      r.discountPercent,
		MilestonesReached:    reached,
		CouponsGenerated:     generated,
		OrdersUntilNext:      n - (u.SuccessfulOrderCount % n),
	}, nil
}

func toDomainUser(u User) domain.User {
	return domain.User{
		ID:                   u.ID,
		Name:                 u.Name,
		Email:                u.Email,
		SuccessfulOrderCount: u.SuccessfulOrderCount,
		CreatedAt:            u.CreatedAt,
	}
}
