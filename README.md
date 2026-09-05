# Mock Ecom Backend

A Go/PostgreSQL ecommerce backend: carts, a two-phase checkout, a mock
payment flow with a provider webhook, per-user discount rewards, order
cancellation and refunds, and admin reporting.

Design rationale, trade-offs and invariants are in
[`DECISIONS.md`](DECISIONS.md).

---

## Tech stack

| Concern | Choice |
|---|---|
| Language | Go 1.25 |
| HTTP | stdlib `net/http` (Go 1.22+ method+path routing) |
| Database | PostgreSQL 16 |
| Data access | GORM |
| Migrations | Hand-written `.sql` + a small runner (no `AutoMigrate`) |
| Config | `.env` + environment variables |
| Container | Multi-stage Dockerfile → ~31 MB Alpine image |

Two direct dependencies: `gorm.io/gorm` and its Postgres driver.

---

## Quick start with Docker

Builds the API image and starts Postgres + the app together. On boot the app
applies migrations, seeds data, and serves on `http://localhost:8080`.

```bash
make docker-up          # docker compose --profile api up -d --build
```

```bash
curl localhost:8080/healthz     # {"status":"ok"}
curl localhost:8080/products    # 5 seeded products
```

```bash
make docker-logs        # follow the API logs
make docker-down        # stop everything and delete the data volume
```

There is no image to pull — `build: .` in `docker-compose.yml` builds it from
the Dockerfile on first run. `--build` forces a rebuild so the container
always runs the code currently on disk.

> `docker compose up -d` **without** `--profile api` starts only the
> database, which is the setup for local Go development below.

---

## Local setup

Postgres in Docker, the API on your host.

**Prerequisites:** Go 1.22+ and Docker Compose.

```bash
# 1. Clone
git clone <repo-url> && cd ecom

# 2. Create .env (defaults match docker-compose; no secrets in it)
make env

# 3. Start Postgres, wait until healthy
make up

# 4. Run the API — migrations and seed run automatically at boot
make run                    # http://localhost:8080
```

There is no separate migrate or seed step. The server applies any unapplied
`migrations/*.sql` (tracked in a `schema_migrations` table) and runs the
idempotent seed on every start.

### Make targets

| Target | What it does |
|---|---|
| `make docker-up` | Build + run Postgres **and** the API in Docker |
| `make docker-logs` | Follow the containerised API logs |
| `make docker-down` | Stop the stack, delete its data |
| `make env` | Create `.env` from `.env.example` |
| `make up` | Start Postgres only |
| `make run` | Run the API on the host |
| `make test` | Run the test suite |
| `make build` | `go build ./...` |
| `make down` | Stop Postgres, delete its data |

---

## Configuration

Lives in `.env` (see `.env.example`). **Real environment variables take
precedence**, so anything can be overridden without editing files:

```bash
REWARD_MILESTONE_N=3 REWARD_DISCOUNT_PERCENT=25 go run ./cmd/server
```

| Variable | Default | Meaning |
|---|---|---|
| `DATABASE_URL` | `postgres://postgres:postgres@localhost:5432/ecom?sslmode=disable` | Postgres connection string |
| `HTTP_ADDR` | `:8080` | API listen address |
| `REWARD_MILESTONE_N` | `5` | **n** — every nth *paid* order by a user mints them a coupon |
| `REWARD_DISCOUNT_PERCENT` | `10` | **x** — percentage off a coupon grants (0–100) |
| `MIGRATIONS_DIR` | `migrations` | Path to migration `.sql` files |
| `SEED_DIR` | `seed` | Path to seed `.sql` files |
| `SKIP_SEED` | `false` | Set `true` to skip seeding |

Invalid reward values make the server fail fast at startup. `n` and `x` are
never stored in the database — only what a user has *earned* is.

---

## Seed data

Applied automatically at startup, idempotent.

**Users** — auth is out of scope, so clients pass `user_id`:

| ID | Name |
|---|---|
| 1 | Aarav Sharma |
| 2 | Priya Iyer |
| 3 | Rohan Gupta |

**Products** — prices in paise (₹899.00 = `89900`):

| ID | Product | Price | Stock |
|---|---|---|---|
| 1 | Wireless Mouse | ₹899.00 | 100 |
| 2 | Mechanical Keyboard | ₹4,499.00 | 50 |
| 3 | USB-C Hub | ₹1,999.00 | 75 |
| 4 | 27" Monitor | ₹18,999.00 | 20 |
| 5 | Limited Edition Webcam | ₹2,499.00 | **3** |

Product 5 has deliberately low stock so out-of-stock behaviour is easy to
exercise.

---

## Architecture

| Layer | Location | Responsibility |
|---|---|---|
| **HTTP** | `internal/httpapi/` | Routing, JSON, error → status mapping. No business logic |
| **Repository** | `internal/repository/` | Business logic + persistence: transactions, row locks, invariants |
| **Domain** | `internal/domain/` | API types and error codes. No dependencies |
| **DB** | `internal/db/` | Connection, migration runner, seeding |

```
Client ──HTTP──> net/http mux ──> handler ──> repository ──> PostgreSQL
                                   (thin)      (transactions + locks)
```

### Data model

```
users ─┬─< carts ──< cart_items >── products
       │      │                          │
       ├─────< orders ──< order_items >──┘
       │         │
       │         └── payments  (1:1, provider_ref UNIQUE)
       │
       └─────< coupons  (owned; user_id NULL = global)
```

Orders embed a **snapshot** of what was bought (product name and unit price),
so they stay explainable after the catalog changes.

---

## API reference

All bodies are JSON. All money is integer **paise**, named `*_paise`. Errors
are `{"code": "...", "message": "..."}` — branch on `code`.

`/admin/*` routes are administrative. **No authentication is implemented**
(out of scope); in a real deployment they would need an admin credential.

### Health

| Method | Path | Description |
|---|---|---|
| `GET` | `/healthz` | Liveness |

### Users and rewards

| Method | Path | Description |
|---|---|---|
| `GET` | `/users` | List seeded users |
| `GET` | `/users/{id}` | One user, incl. paid-order count |
| `GET` | `/users/{id}/rewards` | Milestone progress + coupons owned |

### Products

| Method | Path | Description |
|---|---|---|
| `GET` | `/products` | List all |
| `GET` | `/products/{id}` | One product |

### Cart

| Method | Path | Body | Description |
|---|---|---|---|
| `POST` | `/carts` | `{"user_id": 1}` | Open a cart for a user |
| `GET` | `/carts/{id}` | – | Cart with current prices and subtotal |
| `POST` | `/carts/{id}/items` | `{"product_id": 1, "quantity": 2}` | Add a product |
| `PUT` | `/carts/{id}/items/{productId}` | `{"quantity": 3}` | Set the exact quantity |
| `DELETE` | `/carts/{id}/items/{productId}` | – | Remove a line |

Adding a product already in the cart returns `409 CONFLICT` — use `PUT` to
change quantity.

### Checkout, payment and orders

| Method | Path | Body | Description |
|---|---|---|---|
| `POST` | `/carts/{id}/checkout` | `{"coupon_code": "..."}` *(optional)* | Reserve stock, open a `pending` order + `initiated` payment |
| `POST` | `/payments/webhook` | `{"provider_ref": "pay_...", "status": "success"\|"failed"}` | Provider settles the payment |
| `GET` | `/orders/{id}` | – | Order with items and payment |
| `POST` | `/orders/{id}/cancel` | – | Cancel or refund |

Checkout returns `201` with the pending order and its
`payment.provider_ref`. Calling it again on an already-checked-out cart
returns the **same** order rather than creating a second one.

Cancelling a `pending` order releases its stock and coupon; cancelling a
`paid` order refunds it (stock returns, coupon stays spent).

### Two kinds of coupon

| | **Milestone (automatic)** | **Admin (manual)** |
|---|---|---|
| Created by | The payment that completes a user's nth paid order | An admin calling `POST /admin/coupons` |
| Owner | Always the user who earned it | One user, or nobody (global) |
| `source` | `"milestone"` | `"admin"` |
| Counts as reward progress | Yes | No |
| Used for | The reward programme | Goodwill, backfills, promo codes |

Both are **single-use** and both are redeemed the same way — by passing
`coupon_code` at checkout.

There is no "generate my reward" endpoint: a milestone coupon appears on its
own, in the same transaction that marks the order paid, so it exists the
moment it is earned. `POST /admin/coupons` covers what automation should not
decide for you.

### Admin

| Method | Path | Description |
|---|---|---|
| `POST` | `/admin/coupons` | Create a coupon manually |
| `GET` | `/admin/coupons/{code}` | Look up a coupon |
| `GET` | `/admin/report` | Reporting summary over paid orders |

`POST /admin/coupons` takes:

| Body | Creates |
|---|---|
| `{"user_id": 1}` | A coupon owned by user 1 |
| `{}` | A **global** coupon any customer may redeem |
| `{"code": "DIWALI15", "discount_percent": 15}` | A global coupon with a chosen code and rate |

`user_id` omitted → global · `discount_percent` omitted → configured `x` ·
`code` omitted → generated. Every coupon is single-use.

```jsonc
// GET /admin/report  — counts PAID orders only
{
  "by_product": [ { "product_id": 1, "product_name": "Wireless Mouse",
                    "quantity_sold": 5, "gross_revenue_paise": 449500 } ],
  "gross_revenue_paise": 899400, "total_discount_paise": 8990,
  "net_revenue_paise": 890410,
  "coupons_generated": 1, "coupons_available": 0, "coupons_redeemed": 1,
  "total_orders": 6
}
```

---

## Checkout and payment flow

Payment settles asynchronously, so checkout **reserves** rather than
completes:

```
POST /carts/1/checkout      → stock reserved, order pending, payment initiated
POST /payments/webhook
    status=success          → order paid, reward counter +1, coupon minted if due
    status=failed           → stock and coupon released, order failed
POST /orders/1/cancel       → same release, on demand
```

An order counts toward rewards only once it is **paid**, and the admin report
counts paid orders only.

### Worked example

```bash
USER=1

CART=$(curl -s -X POST localhost:8080/carts -d "{\"user_id\":$USER}" | jq -r .id)
curl -s -X POST localhost:8080/carts/$CART/items -d '{"product_id":1,"quantity":2}'

REF=$(curl -s -X POST localhost:8080/carts/$CART/checkout | jq -r .payment.provider_ref)
curl -s -X POST localhost:8080/payments/webhook \
  -d "{\"provider_ref\":\"$REF\",\"status\":\"success\"}"

curl -s localhost:8080/users/$USER/rewards
curl -s localhost:8080/admin/report
```

---

## Error codes

| Code | Status | When |
|---|---|---|
| `VALIDATION_ERROR` | 400 | Bad quantity, unknown product, empty cart, malformed id |
| `INVALID_COUPON` | 400 | Coupon code does not exist |
| `COUPON_NOT_OWNED` | 403 | The coupon belongs to another user |
| `NOT_FOUND` | 404 | Unknown cart, order, product, user or coupon |
| `CART_ALREADY_CHECKED_OUT` | 409 | Editing a checked-out cart |
| `INSUFFICIENT_STOCK` | 409 | Not enough inventory |
| `COUPON_ALREADY_REDEEMED` | 409 | Coupon already used |
| `ORDER_NOT_CANCELLABLE` | 409 | Order already failed or cancelled |
| `CONFLICT` | 409 | Product already in cart; duplicate coupon code |
| `INTERNAL_ERROR` | 500 | Unrecognised failure |

---

## Testing

```bash
make test
```

Tests run against `DATABASE_URL` and **`TRUNCATE` every table** between
cases, so point it at a development database only.

## Postman

A collection covering all 18 routes is in [`postman/`](postman/) — 9 folders,
66 requests. No environment file to import, and it is safe to run repeatedly.

```bash
npx newman run postman/ecom-checkout-rewards.postman_collection.json
```

---

## Project layout

```
cmd/server/          main: config -> connect -> migrate -> seed -> serve
internal/
  config/            .env + environment loading
  db/                connection, migration runner, seeding
  domain/            API types, error codes
  httpapi/           routes, handlers, JSON, error mapping
  repository/        business logic: transactions, locks, invariants
    carts.go         cart mutations
    checkout.go      phase 1 - validate, reserve, open the payment
    payments.go      phase 2 - settle, mint rewards, cancel, release
    coupons.go       admin coupon creation, lookup
    reports.go       read-only aggregation
    models.go        GORM models
migrations/          0001_init.sql - source of truth for the schema
seed/                users + products
postman/             API collection (all 18 routes)
Dockerfile           multi-stage build
docker-compose.yml   db, and api under the "api" profile
DECISIONS.md         invariants, trade-offs, what was deferred and why
```
