package repository

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/ayush/checkout-rewards/internal/domain"
	"gorm.io/gorm"
)

// Checkout validates a cart and opens a PENDING order with its payment.
// If the cart was already checked out (a client retry after a timeout), it
// returns the original order and changes nothing.
//
// Payment settles asynchronously, so checkout RESERVES rather than
// completes: stock is decremented and the coupon is marked redeemed here,
// and both are released if the payment later fails or the order is
// cancelled. Reserving up front is what keeps "never oversell" true -- the
// alternative (decrement on payment success) lets everyone pay for the last
// item and then refunds all but one. The reward counter is NOT touched
// here; an order only counts once it is paid.
//
// It all runs in one transaction, so any failure rolls back everything: no
// inventory reserved, no coupon consumed, no order created.
//
// Concurrency:
//   - same cart twice: the second call blocks on the cart lock, then sees
//     status=checked_out and replays the existing order
//   - same product from different carts: product rows are locked in
//     product_id order, so stock cannot be oversold and locks cannot deadlock
//   - same coupon from two of the owner's own carts: the coupon row lock
//     means only one checkout can see it as 'available'
func (r *Repo) Checkout(ctx context.Context, cartID int64, couponCode *string) (domain.Order, error) {
	var result domain.Order

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// The cart lock is the idempotency gate.
		var cart Cart
		err := forUpdate(tx).Where("id = ?", cartID).First(&cart).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return domain.NewError(domain.CodeNotFound, "cart not found")
		}
		if err != nil {
			return domain.WrapError(domain.CodeInternal, "lock cart", err)
		}

		if cart.Status == string(domain.CartCheckedOut) {
			result, err = getOrderBy(tx, "cart_id = ?", cartID)
			return err
		}

		// ORDER BY product_id gives every checkout the same lock order.
		var lines []CartItem
		if err := tx.Where("cart_id = ?", cartID).Order("product_id").Find(&lines).Error; err != nil {
			return domain.WrapError(domain.CodeInternal, "list cart items", err)
		}
		if len(lines) == 0 {
			return domain.NewError(domain.CodeValidation, "cart is empty")
		}

		var subtotal int64
		items := make([]domain.OrderItem, 0, len(lines))
		for _, line := range lines {
			var product Product
			err := forUpdate(tx).Where("id = ?", line.ProductID).First(&product).Error
			if err != nil {
				return domain.WrapError(domain.CodeInternal, "lock product", err)
			}
			if product.Inventory < line.Quantity {
				return domain.NewError(domain.CodeInsufficientStock,
					fmt.Sprintf("insufficient stock for %q: requested %d, available %d",
						product.Name, line.Quantity, product.Inventory))
			}

			lineTotal := product.PricePaise * int64(line.Quantity)
			subtotal += lineTotal
			items = append(items, domain.OrderItem{
				ProductID:      line.ProductID,
				ProductName:    product.Name,
				UnitPricePaise: product.PricePaise,
				Quantity:       line.Quantity,
				LineTotalPaise: lineTotal,
			})
		}

		var couponID *int64
		var discountPercent int
		var appliedCode *string
		if couponCode != nil && *couponCode != "" {
			var coupon Coupon
			err := forUpdate(tx).Where("code = ?", *couponCode).First(&coupon).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return domain.NewError(domain.CodeInvalidCoupon, "coupon code not found")
			}
			if err != nil {
				return domain.WrapError(domain.CodeInternal, "lock coupon", err)
			}
			// An owned coupon is redeemable only by its owner. A global
			// coupon (user_id NULL) has no owner, so anyone may redeem it --
			// still only once, since status is what gets consumed.
			if coupon.UserID != nil && *coupon.UserID != cart.UserID {
				return domain.NewError(domain.CodeCouponNotOwned,
					"this coupon belongs to a different user")
			}
			if coupon.Status != string(domain.CouponAvailable) {
				return domain.NewError(domain.CodeCouponAlreadyRedeemed, "coupon has already been redeemed")
			}
			couponID = &coupon.ID
			discountPercent = coupon.DiscountPercent
			appliedCode = couponCode
		}

		// Integer paise only. discountPercent <= 100 and subtotal >= 0, so
		// the total can never go negative.
		discount := (subtotal * int64(discountPercent)) / 100
		total := subtotal - discount

		for _, line := range lines {
			// The inventory >= quantity predicate is a safety net; the row
			// lock above already guarantees it.
			res := tx.Model(&Product{}).
				Where("id = ? AND inventory >= ?", line.ProductID, line.Quantity).
				Updates(map[string]any{
					"inventory":  gorm.Expr("inventory - ?", line.Quantity),
					"updated_at": gorm.Expr("now()"),
				})
			if res.Error != nil {
				return domain.WrapError(domain.CodeInternal, "decrement inventory", res.Error)
			}
			if res.RowsAffected == 0 {
				return domain.NewError(domain.CodeInsufficientStock, "insufficient stock (race)")
			}
		}

		createdAt := time.Now().UTC()
		order := Order{
			UserID:        cart.UserID,
			CartID:        cartID,
			Status:        string(domain.OrderPending),
			SubtotalPaise: subtotal,
			DiscountPaise: discount,
			TotalPaise:    total,
			CouponID:      couponID,
			CouponCode:    appliedCode,
			CreatedAt:     createdAt,
		}
		if err := tx.Create(&order).Error; err != nil {
			return domain.WrapError(domain.CodeInternal, "insert order", err)
		}
		orderID := order.ID // assigned by the database

		// Line items snapshot name and price so the order stays explainable
		// if the catalog changes later.
		orderItems := make([]OrderItem, 0, len(items))
		for _, it := range items {
			orderItems = append(orderItems, OrderItem{
				OrderID:        orderID,
				ProductID:      it.ProductID,
				ProductName:    it.ProductName,
				UnitPricePaise: it.UnitPricePaise,
				Quantity:       it.Quantity,
				LineTotalPaise: it.LineTotalPaise,
			})
		}
		if err := tx.Create(&orderItems).Error; err != nil {
			return domain.WrapError(domain.CodeInternal, "insert order items", err)
		}

		// Open the payment. provider_ref is what the provider will quote back
		// in its webhook, and its UNIQUE constraint makes redelivery safe.
		providerRef, err := newProviderRef()
		if err != nil {
			return domain.WrapError(domain.CodeInternal, "generate provider ref", err)
		}
		payment := Payment{
			OrderID:     orderID,
			AmountPaise: total,
			Status:      string(domain.PaymentInitiated),
			ProviderRef: providerRef,
			CreatedAt:   createdAt,
		}
		if err := tx.Create(&payment).Error; err != nil {
			return domain.WrapError(domain.CodeInternal, "insert payment", err)
		}

		if couponID != nil {
			if err := tx.Model(&Coupon{}).Where("id = ?", *couponID).
				Updates(map[string]any{
					"status":      string(domain.CouponRedeemed),
					"order_id":    orderID,
					"redeemed_at": gorm.Expr("now()"),
				}).Error; err != nil {
				return domain.WrapError(domain.CodeInternal, "redeem coupon", err)
			}
		}

		if err := tx.Model(&Cart{}).Where("id = ?", cartID).
			Updates(map[string]any{
				"status":     string(domain.CartCheckedOut),
				"updated_at": gorm.Expr("now()"),
			}).Error; err != nil {
			return domain.WrapError(domain.CodeInternal, "mark cart checked out", err)
		}

		result = domain.Order{
			ID:            orderID,
			UserID:        cart.UserID,
			CartID:        cartID,
			Status:        domain.OrderStatus(order.Status),
			SubtotalPaise: subtotal,
			DiscountPaise: discount,
			TotalPaise:    total,
			CouponCode:    appliedCode,
			Items:         items,
			Payment:       toDomainPayment(payment),
			CreatedAt:     createdAt,
		}
		return nil
	})

	if err != nil {
		return domain.Order{}, err
	}
	return result, nil
}

func (r *Repo) GetOrder(ctx context.Context, orderID int64) (domain.Order, error) {
	return getOrderBy(r.db.WithContext(ctx), "id = ?", orderID)
}

func getOrderBy(tx *gorm.DB, where string, value any) (domain.Order, error) {
	var o Order
	err := tx.Where(where, value).First(&o).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.Order{}, domain.NewError(domain.CodeNotFound, "order not found")
	}
	if err != nil {
		return domain.Order{}, domain.WrapError(domain.CodeInternal, "get order", err)
	}

	var rows []OrderItem
	if err := tx.Where("order_id = ?", o.ID).Order("product_name").Find(&rows).Error; err != nil {
		return domain.Order{}, domain.WrapError(domain.CodeInternal, "list order items", err)
	}

	var payment Payment
	if err := tx.Where("order_id = ?", o.ID).First(&payment).Error; err != nil &&
		!errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.Order{}, domain.WrapError(domain.CodeInternal, "load payment", err)
	}

	items := make([]domain.OrderItem, 0, len(rows))
	for _, it := range rows {
		items = append(items, domain.OrderItem{
			ProductID:      it.ProductID,
			ProductName:    it.ProductName,
			UnitPricePaise: it.UnitPricePaise,
			Quantity:       it.Quantity,
			LineTotalPaise: it.LineTotalPaise,
		})
	}

	return domain.Order{
		ID:              o.ID,
		UserID:          o.UserID,
		CartID:          o.CartID,
		Status:          domain.OrderStatus(o.Status),
		UserOrderNumber: o.UserOrderNumber,
		SubtotalPaise:   o.SubtotalPaise,
		DiscountPaise:   o.DiscountPaise,
		TotalPaise:      o.TotalPaise,
		CouponCode:      o.CouponCode,
		Items:           items,
		Payment:         toDomainPayment(payment),
		CreatedAt:       o.CreatedAt,
		PaidAt:          o.PaidAt,
		CancelledAt:     o.CancelledAt,
	}, nil
}

func toDomainPayment(p Payment) *domain.Payment {
	if p.ID == 0 {
		return nil
	}
	return &domain.Payment{
		ID:          p.ID,
		OrderID:     p.OrderID,
		AmountPaise: p.AmountPaise,
		Status:      domain.PaymentStatus(p.Status),
		ProviderRef: p.ProviderRef,
		CreatedAt:   p.CreatedAt,
		SettledAt:   p.SettledAt,
	}
}

// newProviderRef is the reference a payment provider would quote back in its
// webhook. It is UNIQUE, which is what makes settlement idempotent.
func newProviderRef() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("pay_%x", b), nil
}
