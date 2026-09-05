-- 0001_init.sql
-- Core schema for the checkout and rewards service.
--
-- MONEY: every amount is stored as an integer number of PAISE (1 rupee =
-- 100 paise) in a BIGINT column. Paise is the smallest indivisible unit of
-- INR, so all arithmetic is exact integer arithmetic and no floating point
-- is involved anywhere. e.g. Rs 899.00 is stored as 89900.
--
-- REWARDS ARE PER USER: each user accumulates their own count of
-- successfully placed orders, and coupons are owned by the user who earned
-- them. See DECISIONS.md "Rewards are user-level, not global".
--
-- The reward parameters (n, x) are NOT stored here -- they are deployment
-- configuration, read from the environment. Only earned state lives in the
-- database: each user's order count, and the coupons already minted.
--
-- IDs are plain sequential integers (BIGSERIAL) rather than UUIDs, which
-- keeps them readable in examples and logs. They are therefore guessable;
-- that is acceptable here only because authentication is out of scope. A
-- deployment with real users would either keep UUIDs for externally visible
-- resources or authorise every lookup.
--
-- This file is the source of truth for the schema. The GORM models in
-- internal/repository/models.go map onto these tables; AutoMigrate is
-- deliberately NOT used, because the CHECK and UNIQUE constraints below are
-- load-bearing -- they make invariant violations physically impossible even
-- if the application logic has a bug.

CREATE TABLE IF NOT EXISTS users (
    id                     BIGSERIAL PRIMARY KEY,
    name                   TEXT NOT NULL,
    email                  TEXT NOT NULL UNIQUE,
    -- This user's own count of successfully placed orders. Incremented
    -- inside the checkout transaction, so "this is my Nth order" is
    -- assigned atomically. Locking this row also serializes a single
    -- user's concurrent checkouts without affecting other users.
    successful_order_count BIGINT NOT NULL DEFAULT 0 CHECK (successful_order_count >= 0),
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS products (
    id             BIGSERIAL PRIMARY KEY,
    name           TEXT NOT NULL,
    price_paise    BIGINT NOT NULL CHECK (price_paise >= 0),
    inventory      INTEGER NOT NULL CHECK (inventory >= 0), -- can never go negative
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS carts (
    id             BIGSERIAL PRIMARY KEY,
    user_id        BIGINT NOT NULL REFERENCES users(id),
    status         TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'checked_out')),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS cart_items (
    id             BIGSERIAL PRIMARY KEY,
    cart_id        BIGINT NOT NULL REFERENCES carts(id) ON DELETE CASCADE,
    product_id     BIGINT NOT NULL REFERENCES products(id),
    quantity       INTEGER NOT NULL CHECK (quantity > 0),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (cart_id, product_id)
);

-- Coupons come from two places:
--   'milestone' - minted automatically when a payment completes a user's nth
--                 paid order. Always owned; always carries a milestone number.
--   'admin'     - created by an administrator. Owned if user_id is set
--                 (a goodwill grant or a backfill), or GLOBAL if user_id is
--                 NULL, in which case any customer may redeem it.
-- Either way a coupon is single-use: available -> redeemed, once.
CREATE TABLE IF NOT EXISTS coupons (
    id                BIGSERIAL PRIMARY KEY,
    -- Owner. NULL means a global coupon that anyone may redeem.
    user_id           BIGINT REFERENCES users(id),
    code              TEXT NOT NULL UNIQUE,
    -- Which of that user's milestones this rewarded. NULL for admin coupons.
    milestone_number  BIGINT,
    source            TEXT NOT NULL DEFAULT 'milestone'
                      CHECK (source IN ('milestone', 'admin')),
    discount_percent  INTEGER NOT NULL CHECK (discount_percent >= 0 AND discount_percent <= 100),
    status            TEXT NOT NULL DEFAULT 'available' CHECK (status IN ('available', 'redeemed')),
    order_id          BIGINT, -- set when redeemed; FK added after orders table exists
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    redeemed_at       TIMESTAMPTZ,
    -- A milestone coupon must be owned and numbered; an admin one must not
    -- carry a milestone number.
    CHECK ((source = 'milestone' AND user_id IS NOT NULL AND milestone_number IS NOT NULL)
        OR (source = 'admin'     AND milestone_number IS NULL)),
    -- One coupon per user per milestone, enforced by the database itself.
    -- NULLs do not collide, so admin coupons are unaffected.
    UNIQUE (user_id, milestone_number)
);

CREATE TABLE IF NOT EXISTS orders (
    id                 BIGSERIAL PRIMARY KEY,
    user_id            BIGINT NOT NULL REFERENCES users(id),
    -- One order per cart, enforced by the database itself: this is what
    -- makes a retried checkout physically unable to create a second order.
    cart_id            BIGINT NOT NULL UNIQUE REFERENCES carts(id),
    -- This user's own order sequence position, which drives milestone logic.
    -- NULL until the order is paid: an order only counts toward rewards once
    -- payment succeeds, so the position is assigned at that moment.
    user_order_number  BIGINT CHECK (user_order_number IS NULL OR user_order_number > 0),
    status             TEXT NOT NULL DEFAULT 'pending'
                       CHECK (status IN ('pending', 'paid', 'failed', 'cancelled')),
    subtotal_paise     BIGINT NOT NULL CHECK (subtotal_paise >= 0),
    discount_paise     BIGINT NOT NULL DEFAULT 0 CHECK (discount_paise >= 0),
    total_paise        BIGINT NOT NULL CHECK (total_paise >= 0), -- never negative
    coupon_id          BIGINT REFERENCES coupons(id),
    coupon_code        TEXT, -- snapshot, in case coupon row changes later
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    paid_at            TIMESTAMPTZ,
    cancelled_at       TIMESTAMPTZ,
    -- NULLs do not collide in a UNIQUE index, so unpaid orders coexist.
    UNIQUE (user_id, user_order_number)
);

-- Payments are async: checkout creates one in 'initiated', and a provider
-- webhook later settles it. provider_ref is the provider's own identifier
-- and is UNIQUE, which is what makes webhook delivery idempotent -- a
-- provider that delivers the same event twice cannot apply it twice.
CREATE TABLE IF NOT EXISTS payments (
    id            BIGSERIAL PRIMARY KEY,
    order_id      BIGINT NOT NULL UNIQUE REFERENCES orders(id) ON DELETE CASCADE,
    amount_paise  BIGINT NOT NULL CHECK (amount_paise >= 0),
    status        TEXT NOT NULL DEFAULT 'initiated'
                  CHECK (status IN ('initiated', 'success', 'failed')),
    provider_ref  TEXT NOT NULL UNIQUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    settled_at    TIMESTAMPTZ
);

ALTER TABLE coupons
    ADD CONSTRAINT coupons_order_id_fkey FOREIGN KEY (order_id) REFERENCES orders(id);

-- Order line items snapshot product name/price at the time of purchase so
-- that an order remains fully explainable even if the product catalog
-- changes afterwards.
CREATE TABLE IF NOT EXISTS order_items (
    id                BIGSERIAL PRIMARY KEY,
    order_id          BIGINT NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    product_id        BIGINT NOT NULL REFERENCES products(id),
    product_name      TEXT NOT NULL,
    unit_price_paise  BIGINT NOT NULL CHECK (unit_price_paise >= 0),
    quantity          INTEGER NOT NULL CHECK (quantity > 0),
    line_total_paise  BIGINT NOT NULL CHECK (line_total_paise >= 0)
);

CREATE INDEX IF NOT EXISTS idx_carts_user_id ON carts(user_id);
CREATE INDEX IF NOT EXISTS idx_cart_items_cart_id ON cart_items(cart_id);
CREATE INDEX IF NOT EXISTS idx_order_items_order_id ON order_items(order_id);
CREATE INDEX IF NOT EXISTS idx_orders_cart_id ON orders(cart_id);
CREATE INDEX IF NOT EXISTS idx_orders_user_id ON orders(user_id);
CREATE INDEX IF NOT EXISTS idx_coupons_user_id ON coupons(user_id);
CREATE INDEX IF NOT EXISTS idx_orders_status ON orders(status);
