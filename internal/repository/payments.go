package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ayush/checkout-rewards/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SettlePayment applies a provider webhook: it settles the payment named by
// providerRef and moves its order to paid or failed.
//
// Idempotency: the payment row is locked and its status checked. A provider
// that delivers the same event twice (or two deliveries racing) finds the
// payment already settled and gets the current state back with no second
// effect -- the same read-check-act-under-lock pattern used for checkout
// retries. providerRef is UNIQUE, so a payment can never be duplicated.
//
// On success the order becomes paid and ONLY THEN does the user's reward
// counter advance -- an order counts toward rewards when it is paid for.
// On failure the reservation is released: stock goes back and the coupon
// becomes available again.
func (r *Repo) SettlePayment(ctx context.Context, providerRef string, succeeded bool) (domain.Order, error) {
	var result domain.Order

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var payment Payment
		err := forUpdate(tx).Where("provider_ref = ?", providerRef).First(&payment).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return domain.NewError(domain.CodeNotFound, "no payment found for that provider reference")
		}
		if err != nil {
			return domain.WrapError(domain.CodeInternal, "lock payment", err)
		}

		// Already settled: replay, changing nothing.
		if payment.Status != string(domain.PaymentInitiated) {
			result, err = getOrderBy(tx, "id = ?", payment.OrderID)
			return err
		}

		var order Order
		if err := forUpdate(tx).Where("id = ?", payment.OrderID).First(&order).Error; err != nil {
			return domain.WrapError(domain.CodeInternal, "lock order", err)
		}

		now := time.Now().UTC()

		if !succeeded {
			if err := releaseReservation(tx, order); err != nil {
				return err
			}
			if err := tx.Model(&Payment{}).Where("id = ?", payment.ID).
				Updates(map[string]any{"status": string(domain.PaymentFailed), "settled_at": now}).Error; err != nil {
				return domain.WrapError(domain.CodeInternal, "fail payment", err)
			}
			if err := tx.Model(&Order{}).Where("id = ?", order.ID).
				Update("status", string(domain.OrderFailed)).Error; err != nil {
				return domain.WrapError(domain.CodeInternal, "fail order", err)
			}
			result, err = getOrderBy(tx, "id = ?", order.ID)
			return err
		}

		// Paid: this is the moment the order becomes a "successfully placed
		// order", so claim this user's next reward position atomically.
		var user User
		res := tx.Model(&user).
			Clauses(clause.Returning{Columns: []clause.Column{{Name: "successful_order_count"}}}).
			Where("id = ?", order.UserID).
			Update("successful_order_count", gorm.Expr("successful_order_count + 1"))
		if res.Error != nil {
			return domain.WrapError(domain.CodeInternal, "increment user order counter", res.Error)
		}

		// Minting the reward here makes it atomic with the order becoming
		// paid: the coupon exists the instant it is earned, and a settlement
		// that rolls back takes the coupon with it.
		if err := r.mintMilestoneCoupon(tx, order.UserID, user.SuccessfulOrderCount); err != nil {
			return err
		}

		if err := tx.Model(&Payment{}).Where("id = ?", payment.ID).
			Updates(map[string]any{"status": string(domain.PaymentSuccess), "settled_at": now}).Error; err != nil {
			return domain.WrapError(domain.CodeInternal, "settle payment", err)
		}
		if err := tx.Model(&Order{}).Where("id = ?", order.ID).
			Updates(map[string]any{
				"status":            string(domain.OrderPaid),
				"user_order_number": user.SuccessfulOrderCount,
				"paid_at":           now,
			}).Error; err != nil {
			return domain.WrapError(domain.CodeInternal, "mark order paid", err)
		}

		result, err = getOrderBy(tx, "id = ?", order.ID)
		return err
	})

	if err != nil {
		return domain.Order{}, err
	}
	return result, nil
}

// mintMilestoneCoupon issues this user's reward when the order just paid for
// completes a milestone -- their nth, 2nth, 3nth and so on.
//
// Concurrency: the caller already holds this user's row lock (it was taken
// to increment the counter), so two settlements for the same user cannot
// both compute the same position. UNIQUE (user_id, milestone_number) is the
// storage-level backstop.
//
// ON CONFLICT DO NOTHING matters for a specific reason: lowering n after
// coupons exist can make an already-rewarded milestone number come round
// again, and a unique violation there would fail the whole payment. A
// reward is never worth failing a settlement over.
func (r *Repo) mintMilestoneCoupon(tx *gorm.DB, userID, paidOrderCount int64) error {
	n := int64(r.milestoneEveryN)
	if paidOrderCount%n != 0 {
		return nil // not a milestone order
	}

	milestone := paidOrderCount / n
	code, err := generateCouponCode(milestone)
	if err != nil {
		return domain.WrapError(domain.CodeInternal, "generate coupon code", err)
	}

	coupon := Coupon{
		UserID:          &userID,
		Code:            code,
		MilestoneNumber: &milestone,
		Source:          string(domain.CouponFromMilestone),
		DiscountPercent: r.discountPercent,
		Status:          string(domain.CouponAvailable),
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&coupon).Error; err != nil {
		return domain.WrapError(domain.CodeInternal, "mint milestone coupon", err)
	}
	return nil
}

// CancelOrder cancels an order and releases what it was holding.
//
// A pending order releases its stock and its coupon -- nothing was ever
// completed. A paid order is a refund: stock returns to the catalog, but the
// coupon stays spent and the user's reward count is NOT decremented, so a
// milestone already earned can never be revoked (which would strand a coupon
// that has already been minted or spent). See DECISIONS.md.
func (r *Repo) CancelOrder(ctx context.Context, orderID int64) (domain.Order, error) {
	var result domain.Order

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var order Order
		err := forUpdate(tx).Where("id = ?", orderID).First(&order).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return domain.NewError(domain.CodeNotFound, "order not found")
		}
		if err != nil {
			return domain.WrapError(domain.CodeInternal, "lock order", err)
		}

		switch domain.OrderStatus(order.Status) {
		case domain.OrderPending:
			if err := releaseReservation(tx, order); err != nil {
				return err
			}
			// The payment is abandoned rather than settled.
			if err := tx.Model(&Payment{}).Where("order_id = ? AND status = ?",
				order.ID, string(domain.PaymentInitiated)).
				Update("status", string(domain.PaymentFailed)).Error; err != nil {
				return domain.WrapError(domain.CodeInternal, "abandon payment", err)
			}
		case domain.OrderPaid:
			// Refund: restock only. The coupon stays spent and rewards stand.
			if err := restoreStock(tx, order.ID); err != nil {
				return err
			}
		default:
			return domain.NewError(domain.CodeOrderNotCancellable,
				fmt.Sprintf("an order with status %q cannot be cancelled", order.Status))
		}

		if err := tx.Model(&Order{}).Where("id = ?", order.ID).
			Updates(map[string]any{
				"status":       string(domain.OrderCancelled),
				"cancelled_at": time.Now().UTC(),
			}).Error; err != nil {
			return domain.WrapError(domain.CodeInternal, "cancel order", err)
		}

		result, err = getOrderBy(tx, "id = ?", order.ID)
		return err
	})

	if err != nil {
		return domain.Order{}, err
	}
	return result, nil
}

// releaseReservation returns everything a pending order was holding: its
// stock and, if it used one, its coupon.
func releaseReservation(tx *gorm.DB, order Order) error {
	if err := restoreStock(tx, order.ID); err != nil {
		return err
	}
	if order.CouponID == nil {
		return nil
	}
	// Back to available, and unlinked from the order that never completed.
	err := tx.Model(&Coupon{}).Where("id = ?", *order.CouponID).
		Updates(map[string]any{
			"status":      string(domain.CouponAvailable),
			"order_id":    nil,
			"redeemed_at": nil,
		}).Error
	if err != nil {
		return domain.WrapError(domain.CodeInternal, "release coupon", err)
	}
	return nil
}

// restoreStock puts an order's quantities back. Products are locked in
// product_id order, matching checkout, so the two cannot deadlock.
func restoreStock(tx *gorm.DB, orderID int64) error {
	var items []OrderItem
	if err := tx.Where("order_id = ?", orderID).Order("product_id").Find(&items).Error; err != nil {
		return domain.WrapError(domain.CodeInternal, "list order items", err)
	}
	for _, item := range items {
		var product Product
		if err := forUpdate(tx).Where("id = ?", item.ProductID).First(&product).Error; err != nil {
			return domain.WrapError(domain.CodeInternal, "lock product", err)
		}
		err := tx.Model(&Product{}).Where("id = ?", item.ProductID).
			Updates(map[string]any{
				"inventory":  gorm.Expr("inventory + ?", item.Quantity),
				"updated_at": gorm.Expr("now()"),
			}).Error
		if err != nil {
			return domain.WrapError(domain.CodeInternal, "restore inventory", err)
		}
	}
	return nil
}
