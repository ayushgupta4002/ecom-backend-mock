package repository

import "time"

// GORM models mapping onto the schema defined in migrations/0001_init.sql.
//
// Column names are given explicitly rather than relying on GORM's
// name-inference, so that the mapping between a Go field and a database
// column is unambiguous when reading either file. AutoMigrate is
// deliberately not used: the SQL migration is the source of truth, because
// its CHECK/UNIQUE constraints are part of how the service's invariants are
// enforced.
//
// All monetary columns are integer PAISE (1 rupee = 100 paise).

type User struct {
	ID                   int64     `gorm:"column:id;primaryKey"`
	Name                 string    `gorm:"column:name"`
	Email                string    `gorm:"column:email"`
	SuccessfulOrderCount int64     `gorm:"column:successful_order_count"`
	CreatedAt            time.Time `gorm:"column:created_at"`
	UpdatedAt            time.Time `gorm:"column:updated_at"`
}

func (User) TableName() string { return "users" }

type Product struct {
	ID         int64     `gorm:"column:id;primaryKey"`
	Name       string    `gorm:"column:name"`
	PricePaise int64     `gorm:"column:price_paise"`
	Inventory  int       `gorm:"column:inventory"`
	CreatedAt  time.Time `gorm:"column:created_at"`
	UpdatedAt  time.Time `gorm:"column:updated_at"`
}

func (Product) TableName() string { return "products" }

type Cart struct {
	ID        int64     `gorm:"column:id;primaryKey"`
	UserID    int64     `gorm:"column:user_id"`
	Status    string    `gorm:"column:status"`
	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

func (Cart) TableName() string { return "carts" }

type CartItem struct {
	ID        int64     `gorm:"column:id;primaryKey"`
	CartID    int64     `gorm:"column:cart_id"`
	ProductID int64     `gorm:"column:product_id"`
	Quantity  int       `gorm:"column:quantity"`
	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

func (CartItem) TableName() string { return "cart_items" }

type Coupon struct {
	ID              int64      `gorm:"column:id;primaryKey"`
	UserID          *int64     `gorm:"column:user_id"`
	Code            string     `gorm:"column:code"`
	MilestoneNumber *int64     `gorm:"column:milestone_number"`
	Source          string     `gorm:"column:source"`
	DiscountPercent int        `gorm:"column:discount_percent"`
	Status          string     `gorm:"column:status"`
	OrderID         *int64     `gorm:"column:order_id"`
	CreatedAt       time.Time  `gorm:"column:created_at"`
	RedeemedAt      *time.Time `gorm:"column:redeemed_at"`
}

func (Coupon) TableName() string { return "coupons" }

type Order struct {
	ID              int64      `gorm:"column:id;primaryKey"`
	UserID          int64      `gorm:"column:user_id"`
	CartID          int64      `gorm:"column:cart_id"`
	UserOrderNumber *int64     `gorm:"column:user_order_number"`
	Status          string     `gorm:"column:status"`
	SubtotalPaise   int64      `gorm:"column:subtotal_paise"`
	DiscountPaise   int64      `gorm:"column:discount_paise"`
	TotalPaise      int64      `gorm:"column:total_paise"`
	CouponID        *int64     `gorm:"column:coupon_id"`
	CouponCode      *string    `gorm:"column:coupon_code"`
	CreatedAt       time.Time  `gorm:"column:created_at"`
	PaidAt          *time.Time `gorm:"column:paid_at"`
	CancelledAt     *time.Time `gorm:"column:cancelled_at"`
}

func (Order) TableName() string { return "orders" }

type Payment struct {
	ID          int64      `gorm:"column:id;primaryKey"`
	OrderID     int64      `gorm:"column:order_id"`
	AmountPaise int64      `gorm:"column:amount_paise"`
	Status      string     `gorm:"column:status"`
	ProviderRef string     `gorm:"column:provider_ref"`
	CreatedAt   time.Time  `gorm:"column:created_at"`
	SettledAt   *time.Time `gorm:"column:settled_at"`
}

func (Payment) TableName() string { return "payments" }

type OrderItem struct {
	ID             int64  `gorm:"column:id;primaryKey"`
	OrderID        int64  `gorm:"column:order_id"`
	ProductID      int64  `gorm:"column:product_id"`
	ProductName    string `gorm:"column:product_name"`
	UnitPricePaise int64  `gorm:"column:unit_price_paise"`
	Quantity       int    `gorm:"column:quantity"`
	LineTotalPaise int64  `gorm:"column:line_total_paise"`
}

func (OrderItem) TableName() string { return "order_items" }
