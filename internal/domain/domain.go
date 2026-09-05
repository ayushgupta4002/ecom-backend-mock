// Package domain contains the API-facing types shared by the repository
// and HTTP layers.
//
// All money is an int64 count of PAISE (1 rupee = 100 paise). Paise is the
// smallest indivisible unit of INR, so every amount is a whole number and
// all arithmetic is exact integer arithmetic -- no floating point is used
// anywhere in a monetary calculation. Fields are named *_paise so the unit
// is never ambiguous. Formatting for display is left to the client.
package domain

import "time"

type User struct {
	ID                   int64     `json:"id"`
	Name                 string    `json:"name"`
	Email                string    `json:"email"`
	SuccessfulOrderCount int64     `json:"successful_order_count"`
	CreatedAt            time.Time `json:"created_at"`
}

// RewardStatus tells a user how far they are from their next coupon.
// Coupons are minted automatically, so CouponsGenerated tracks
// MilestonesReached; they can only diverge if n was changed after coupons
// had already been issued.
type RewardStatus struct {
	UserID               int64 `json:"user_id"`
	SuccessfulOrderCount int64 `json:"successful_order_count"`
	MilestoneEveryN      int   `json:"milestone_every_n"`
	DiscountPercent      int   `json:"discount_percent"`
	MilestonesReached    int64 `json:"milestones_reached"`
	CouponsGenerated     int64 `json:"coupons_generated"`
	UngeneratedCoupons   int64 `json:"coupons_pending_generation"`
	OrdersUntilNext      int64 `json:"orders_until_next_milestone"`
}

type Product struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	PricePaise int64     `json:"price_paise"`
	Inventory  int       `json:"inventory"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type CartStatus string

const (
	CartOpen       CartStatus = "open"
	CartCheckedOut CartStatus = "checked_out"
)

type Cart struct {
	ID        int64      `json:"id"`
	UserID    int64      `json:"user_id"`
	Status    CartStatus `json:"status"`
	Items     []CartItem `json:"items"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// CartItem shows the stored quantity alongside *current* product data, so a
// client can see price/stock drift while shopping. What is actually charged
// is decided at checkout, not here.
type CartItem struct {
	ID                    int64  `json:"id"`
	ProductID             int64  `json:"product_id"`
	ProductName           string `json:"product_name"`
	Quantity              int    `json:"quantity"`
	CurrentUnitPricePaise int64  `json:"current_unit_price_paise"`
	LineTotalPaise        int64  `json:"line_total_paise"`
	AvailableInStock      int    `json:"available_in_stock"`
}

type CartView struct {
	Cart          Cart  `json:"cart"`
	SubtotalPaise int64 `json:"subtotal_paise"`
}

type OrderStatus string

const (
	// OrderPending means the order exists and stock is reserved for it, but
	// payment has not settled yet.
	OrderPending OrderStatus = "pending"
	// OrderPaid is a successfully placed order. Only these count toward
	// rewards and revenue.
	OrderPaid OrderStatus = "paid"
	// OrderFailed means payment failed; the reservation has been released.
	OrderFailed OrderStatus = "failed"
	// OrderCancelled means the customer or an admin cancelled it.
	OrderCancelled OrderStatus = "cancelled"
)

type PaymentStatus string

const (
	PaymentInitiated PaymentStatus = "initiated"
	PaymentSuccess   PaymentStatus = "success"
	PaymentFailed    PaymentStatus = "failed"
)

// Payment is created alongside a pending order. The provider settles it
// asynchronously via the webhook; ProviderRef is the provider's identifier
// and is what makes repeated webhook deliveries idempotent.
type Payment struct {
	ID          int64         `json:"id"`
	OrderID     int64         `json:"order_id"`
	AmountPaise int64         `json:"amount_paise"`
	Status      PaymentStatus `json:"status"`
	ProviderRef string        `json:"provider_ref"`
	CreatedAt   time.Time     `json:"created_at"`
	SettledAt   *time.Time    `json:"settled_at,omitempty"`
}

type OrderItem struct {
	ProductID      int64  `json:"product_id"`
	ProductName    string `json:"product_name"`
	UnitPricePaise int64  `json:"unit_price_paise"`
	Quantity       int    `json:"quantity"`
	LineTotalPaise int64  `json:"line_total_paise"`
}

type Order struct {
	ID     int64       `json:"id"`
	UserID int64       `json:"user_id"`
	CartID int64       `json:"cart_id"`
	Status OrderStatus `json:"status"`
	// UserOrderNumber is this customer's own order position, assigned when
	// payment succeeds. Nil while the order is unpaid.
	UserOrderNumber *int64      `json:"user_order_number,omitempty"`
	SubtotalPaise   int64       `json:"subtotal_paise"`
	DiscountPaise   int64       `json:"discount_paise"`
	TotalPaise      int64       `json:"total_paise"`
	CouponCode      *string     `json:"coupon_code,omitempty"`
	Items           []OrderItem `json:"items"`
	Payment         *Payment    `json:"payment,omitempty"`
	CreatedAt       time.Time   `json:"created_at"`
	PaidAt          *time.Time  `json:"paid_at,omitempty"`
	CancelledAt     *time.Time  `json:"cancelled_at,omitempty"`
}

type CouponStatus string

const (
	CouponAvailable CouponStatus = "available"
	CouponRedeemed  CouponStatus = "redeemed"
)

type CouponSource string

const (
	// CouponFromMilestone is minted automatically by a qualifying payment.
	CouponFromMilestone CouponSource = "milestone"
	// CouponFromAdmin is created by an administrator, either for one user or
	// globally.
	CouponFromAdmin CouponSource = "admin"
)

// Coupon is single-use. UserID is nil for a global coupon, which any
// customer may redeem; MilestoneNumber is nil unless it was earned.
type Coupon struct {
	ID              int64        `json:"id"`
	UserID          *int64       `json:"user_id,omitempty"`
	Global          bool         `json:"global"`
	Source          CouponSource `json:"source"`
	Code            string       `json:"code"`
	MilestoneNumber *int64       `json:"milestone_number,omitempty"`
	DiscountPercent int          `json:"discount_percent"`
	Status          CouponStatus `json:"status"`
	OrderID         *int64       `json:"order_id,omitempty"`
	CreatedAt       time.Time    `json:"created_at"`
	RedeemedAt      *time.Time   `json:"redeemed_at,omitempty"`
}

type ProductRevenue struct {
	ProductID         int64  `json:"product_id"`
	ProductName       string `json:"product_name"`
	QuantitySold      int64  `json:"quantity_sold"`
	GrossRevenuePaise int64  `json:"gross_revenue_paise"`
}

type Report struct {
	ByProduct          []ProductRevenue `json:"by_product"`
	GrossRevenuePaise  int64            `json:"gross_revenue_paise"`
	TotalDiscountPaise int64            `json:"total_discount_paise"`
	NetRevenuePaise    int64            `json:"net_revenue_paise"`
	CouponsGenerated   int64            `json:"coupons_generated"`
	CouponsAvailable   int64            `json:"coupons_available"`
	CouponsRedeemed    int64            `json:"coupons_redeemed"`
	TotalOrders        int64            `json:"total_orders"`
}
