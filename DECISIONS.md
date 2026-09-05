# DECISIONS

Go + Postgres (GORM) checkout and rewards service.

## Invariants

Each one is guarded twice: in code, so the client gets a clear error, and by
a database constraint, so a bug cannot break it silently.

| # | Invariant | Enforced by |
|---|---|---|
| 1 | Stock is never oversold | product `FOR UPDATE` + `UPDATE … WHERE inventory >= ?` + `CHECK (inventory >= 0)` |
| 2 | A cart checks out once, then can't be edited | cart `FOR UPDATE` + `orders.cart_id UNIQUE` |
| 3 | Retrying a checkout changes nothing | one-way `open → checked_out`, read under the cart lock |
| 4 | A coupon is used at most once | coupon `FOR UPDATE` inside the checkout transaction |
| 5 | Only the owner can use an owned coupon | ownership check under that same lock |
| 6 | One reward per user per milestone | minted under the user row lock + `UNIQUE (user_id, milestone_number)` |
| 7 | An order explains itself forever | product name and price copied onto `order_items` |
| 8 | An order total is never negative | integer maths + `CHECK (total_paise >= 0)`, `discount_percent <= 100` |
| 9 | The report always adds up | it reads paid orders and coupons directly, not a separate tally |
| 10 | A webhook delivered twice applies once | payment `FOR UPDATE` + status check, `provider_ref UNIQUE` |
| 11 | A failed or cancelled order holds nothing | stock and coupon released in the same transaction |

## Ambiguities, and what I chose

**Are rewards per user or store-wide?** The brief only says "after every nth
successfully placed order". I chose per user — see decision 1.

**What if a price or stock level changes after you add to the cart?** The
cart shows live prices and stock, but nothing is locked in. Checkout re-reads
both under a lock and charges the current price. Freezing the price at
add-time would let someone buy at a stale price, and stock has to be
re-checked at checkout anyway.

**What if you add a product already in the cart?** `409 CONFLICT`; use `PUT`
to change the quantity. If adding just incremented, a retried `POST` would
double the quantity. `PUT` sets an absolute number, so retrying is safe.

**Who creates the reward coupon?** Nobody. It is minted automatically by the
payment that completes a milestone — see decision 2.

**What shape is a coupon?** A percentage off the subtotal, one per order, no
minimum spend, no expiry. A percentage of a non-negative subtotal is what
makes invariant 8 free.

**Why integer IDs instead of UUIDs?** They keep examples and logs readable.
The cost is that they are guessable: with no authentication, anyone could try
cart 7. That is only acceptable because auth is out of scope.

**Can you check out an empty cart?** No. An order for nothing is not a real
event and would push the reward counter forward.

## Material decisions
### 1. Rewards are per user, not store-wide

**Problem:** My first version counted orders store-wide and let anyone spend
the resulting coupon. It rewarded purchasing activity without rewarding the
purchaser: A's fifth order created a coupon B could spend.

**Options:** (a) global counter, shared coupons; (b) per-user counter, owned
coupons; (c) per-user counter, coupons open to anyone.

**Choice:** (b).

**Why:** A reward that doesn't reach the person who earned it isn't a
reward. (a) also lets anyone who sees a code spend someone else's discount.
(c) is the worst of both: it works out who deserves it, then gives it away.

**Consequence, and a happy one:** this removed a bottleneck. Every checkout
used to contend on a single counter row, serialising the whole store.
Contention is now per user, so two customers never block each other. The
locking strategy itself did not change — same calls, same order, different
rows — and the concurrency tests passed unmodified.

### 2. Rewards are minted automatically, not by an admin clicking a button

**This departs from the brief, on purpose.** The brief lists "generate a
coupon when an unrewarded milestone is eligible" as an admin operation. I
built exactly that first, then removed it as the reward mechanism.

**Problem:** With a manual endpoint, a customer could finish their 5th paid
order and still hold no coupon until someone remembered to click. The reward
existed in the database as an entitlement and as nothing the customer could
use.

**Options:** (a) admin-triggered, as written; (b) automatic; (c) automatic,
with the admin endpoint kept for other uses.

**Choice:** (c).

**Why:** A manual step made sense while coupons were store-wide, because
there was a real question about who the single shared coupon belonged to.
Once rewards became per-user that question disappeared: the coupon belongs to
whoever earned it, and the milestone is arithmetic. There is no judgement
left for a human to apply.

Minting happens inside the settlement transaction, right after the counter is
incremented, so the coupon and the paid order commit together. The user row
is already locked, so two settlements cannot both mint the same milestone;
`UNIQUE (user_id, milestone_number)` is the backstop, and the insert uses
`ON CONFLICT DO NOTHING` so that lowering `n` later can never fail a payment
over a duplicate reward.

`POST /admin/coupons` remains for what automation should not decide: a
goodwill grant, a backfill, or a global promo code. That keeps the brief's
admin operation while taking the human out of the path that needs no human.

**Consequence:** Two error codes and a "pending generation" field became
meaningless and were deleted — a milestone can no longer be reached but
unrewarded. Coupons gained a `source` (`milestone` or `admin`), and reward
progress counts only the earned ones, so a goodwill grant is never mistaken
for something the customer earned.

### 3. Global coupons have no owner but are still single-use

**Problem:** An admin needs promo codes that belong to nobody, alongside
rewards that belong to someone.

**Options:** (a) a separate promotions table; (b) one coupon table where
`user_id` can be NULL; (c) multi-use codes with a redemption limit.

**Choice:** (b), single-use.

**Why:** A coupon is a coupon — same redemption path, same lock, same
"used at most once" rule. A nullable owner says "anyone may redeem this"
without duplicating any logic, and NULLs don't collide in
`UNIQUE (user_id, milestone_number)`, so nothing else had to change. A
`CHECK` keeps the two kinds honest: an earned coupon must have an owner and a
milestone number, an admin one must have no milestone number.

**Trade-off, stated plainly:** this is a bearer coupon, not a broadcast promo
code. `DIWALI10` sent to 1,000 customers would be redeemed by whoever gets
there first and refused for the other 999. Real multi-use campaigns are
option (c): they need a redemption counter with its own contention and
per-customer limits, which is a different feature wearing the same word.

### 4. The cart is the idempotency key

**Problem:** A client whose request times out will retry checkout. That must
not create a second order.

**Options:** (a) a client-supplied `Idempotency-Key` header; (b) use the
cart's own lifecycle.

**Choice:** (b).

**Why:** The server already created a unique, single-use token when it
created the cart — the cart ID. A client-supplied key adds a second
mechanism with its own failure modes (reused for different intents, or
forgotten) to solve a problem already solved. Reading `open → checked_out`
under the cart's row lock is what makes it safe: without the lock, a retry
arriving while the first request is still running would place a second order.

**Trade-off:** This covers "same cart, sent twice". It does not cover a
client that builds a brand new cart for each retry — but a client-supplied
key would not have covered that either.

### 5. One transaction, with row locks

**Problem:** Concurrent checkouts must not oversell or double-spend a
coupon, and a failed checkout must leave nothing behind.

**Options:** (a) optimistic version columns with retry loops; (b)
`SELECT … FOR UPDATE` under `READ COMMITTED`; (c) `SERIALIZABLE` with retries.

**Choice:** (b).

**Why:** The whole checkout is one unit of work, so rollback gives the
failure behaviour for free. The contended rows are few and known — one cart,
one user, N products, one coupon — so locking them directly is cheaper than
(c)'s aborts and simpler than the retry loops (a) needs everywhere.

**Trade-off:** Locks are held until the transaction ends, so two people
racing for the same product or coupon really do queue. That is the intended
behaviour, not a bug.

### 6. Checkout reserves stock; payment settles it

**Problem:** Payment is confirmed asynchronously by a webhook, so checkout
cannot finish an order on its own. When should stock leave the shelf?

**Options:** (a) when payment succeeds; (b) at checkout, released if payment
fails.

**Choice:** (b).

**Why:** Under (a) ten people can all pay for the last item and nine need
refunds. That turns an inventory problem into a money problem and breaks
"never oversell". Reserving up front keeps the invariant and fails early,
before anyone pays.

**Trade-off:** An abandoned pending order holds stock until it is cancelled.
A job that expires stale pending orders is the missing piece, listed under
deferred.

This reverses an earlier decision. When checkout was synchronous I argued
against reserve-then-confirm, because one transaction made "coupon redeemed"
and "order placed" a single fact with no in-between state to leak. That was
right for a synchronous checkout and stopped being right the moment
settlement became asynchronous.

### 7. Money as integer paise

**Problem:** "Calculate money without floating-point rounding errors."

**Options:** (a) `float64` rupees; (b) whole rupees; (c) `NUMERIC(12,2)` and
a decimal library; (d) `int64` paise.

**Choice:** (d).

**Why:** (a) is ruled out by the requirement. (b) is float-free but lossy:
10% off ₹899.00 is ₹89.90, and whole rupees silently drop 90 paise on every
discounted order, which accumulates in the report. (c) is exact but buys
nothing, since every amount is already a whole number of paise, while adding
a dependency and noisier arithmetic.

**Trade-off:** None worth the name. Display formatting is left to the client
so there is one authoritative number on the wire.

### 8. GORM, with locks written out explicitly

**Problem:** The invariants rest on row locks, so the data layer must
express them precisely and keep them visible.

**Options:** (a) raw SQL over `pgx`; (b) GORM with `clause.Locking`; (c) GORM
for simple work, raw SQL inside checkout.

**Choice:** (b).

**Why:** GORM removes real boilerplate, but an ORM can hide the exact thing
being relied on. So nothing is implicit: every lock goes through one
`forUpdate()` helper, and stock is decremented with a conditional
`UPDATE … WHERE inventory >= ?` whose `RowsAffected` is checked — never a
read-modify-write, which is the bug an ORM invites. (c) would put a seam
between two styles right inside the most important function.

**Check, not trust:** I turned on `log_statement = all` and confirmed the
concurrency tests really do emit `SELECT … FOR UPDATE` on the cart and
product rows. `AutoMigrate` is not used — `migrations/0001_init.sql` stays
the source of truth, because its `CHECK` and `UNIQUE` constraints are half
the enforcement and belong in SQL a reviewer can read.

### 9. `n` and `x` live only in the environment

**Problem:** The reward settings are deployment configuration. A user's
order count and their coupons are earned state. Where does each belong?

**Options:** (a) both in the database; (b) both in the environment, with
progress derived from `COUNT(*)`; (c) settings from the environment, earned
state in the database.

**Choice:** (c) — and the settings are not copied into a table at all.

**Why:** An operator changes the programme by editing `.env`, with no
migration. Nothing inside a transaction needs `n` or `x`: checkout only
increments the user's counter, and the discount applied comes from the coupon
row, which stores its own percentage. (b) was rejected because `COUNT(*)` on
the hot path loses the atomic assignment of "this is your Nth order".

**Trade-off:** Changing `x` affects only future coupons. Invalid values stop
the server at startup rather than quietly changing behaviour.

An earlier version did mirror `n`/`x` into a table, justified by "checkout
needs them in its transaction". That stopped being true when rewards became
per-user, and the table survived the change without anyone rechecking whether
it still earned its place. Removing it is the correction.

### 10. Never call the payment provider inside a transaction

**Problem:** You cannot hold Postgres row locks open across a network call
to a payment provider. A slow provider would stall every purchase of the
products in that order.

**Options:** (a) call the provider inside the checkout transaction;
(b) charge first, then open the transaction; (c) record the payment locally
and let the provider settle it later by webhook.

**Choice:** (c).

**Why:** (a) is ruled out by lock duration. (b) leaves a window where money
is taken and the transaction then fails, needing a refund to compensate.
(c) matches how real providers work: our transaction only touches our own
database, and the provider's confirmation arrives as its own short
transaction.

**Consequence:** `provider_ref` is created at checkout, is `UNIQUE`, and is
the idempotency key for settlement. Duplicate, concurrent and out-of-order
deliveries all become the same read-check-act under one row lock as decision
1.

Business logic still sits with the SQL rather than in a separate service
layer, because the interesting logic *is* what happens inside a transaction.
A payment provider would be the first real reason to extract one, since it is
the first dependency that isn't the database.

## Transactions, concurrency and idempotency

One transaction per phase. Locks are taken in a fixed order: cart → products
(ascending `product_id`) → coupon → user. `READ COMMITTED` is enough because
every invariant-relevant read is protected by an explicit `FOR UPDATE`
instead of relying on snapshot isolation.

| Race | Blocks on | Result |
|---|---|---|
| Same cart twice | cart row | Second returns the first's order |
| Same product, different carts | product row | Second re-reads stock after the first commits, fails if short |
| Same coupon twice | coupon row | Exactly one sees it available |
| Two settlements for one user | user row | Different positions, one milestone coupon |
| Same webhook twice | payment row | First settles, the rest change nothing |

`user_order_number` comes from `UPDATE users … RETURNING` at payment success,
which is atomic per user and drives the milestone maths. There is no
store-wide counter to contend on.

Products are always locked in ascending `product_id`, on every path — cart
lines are read in that order, and stock restoration sorts the same way — so
a checkout and a releasing webhook cannot deadlock against each other.

## Money and rounding

`BIGINT` paise in the database, `int64` in Go, fields named `*_paise`.

```
discount = subtotal * percent / 100    (integer division, rounds down)
total    = subtotal - discount
```

Because `percent <= 100` is a constraint and the subtotal is never negative,
the discount can never exceed the subtotal, so the total can never go
negative. No clamping code needed. Example: 10% off ₹899.00 is
`89900 * 10 / 100 = 8990` paise, leaving ₹809.10.

## Error model

Every failure is a `*domain.Error{Code, Message}`. `Code` is a stable enum
clients branch on; `Message` is prose that may change. One table maps code to
HTTP status, so the contract lives in a single place. Anything unrecognised
is logged and returned as a plain `500`.

`400` VALIDATION_ERROR, INVALID_COUPON · `403` COUPON_NOT_OWNED · `404`
NOT_FOUND · `409` CART_ALREADY_CHECKED_OUT, INSUFFICIENT_STOCK,
COUPON_ALREADY_REDEEMED, ORDER_NOT_CANCELLABLE, CONFLICT · `500`
INTERNAL_ERROR

## Built vs deferred

**Built:** cart management with validation; checkout with retry safety and
no overselling; asynchronous payment with an idempotent webhook; the order
lifecycle (`pending/paid/failed/cancelled`) with cancellation and refunds
that release what they held; per-user rewards minted automatically; admin and
global coupons; a report that reconciles; exact paise accounting; migrations
and seed data; environment config that fails fast; tests covering concurrent
and repeated requests.

**Deferred:**

- **Authentication.** Out of scope. Clients pass `user_id`, so coupon
  ownership is an integrity check, not a security boundary. With sessions the
  check compares the session's user instead — the check itself doesn't move.
- **A real payment provider.** There is no signature check on the webhook,
  which production would need, and no actual refund call.
- **Expiring stale pending orders.** Abandoned reservations hold stock until
  someone cancels them. This is the debt created by decision 6.
- **A separate test database.** Tests share the dev database and truncate it.
  CI should get an ephemeral one per run.
- **Monitoring.** Nothing is instrumented yet. I would expose Prometheus
  metrics on `/metrics` and read them in Grafana. This service is mostly
  about contention, and contention is invisible without numbers: the ones
  worth watching are checkout outcomes split by result (placed, out of
  stock, coupon conflict), how long a checkout holds its transaction, and
  how often webhooks arrive as duplicates. A rising conflict rate is the
  earliest sign that something is wrong.
- **Multi-use promo codes, coupon and cart expiry, down migrations,
  pagination, structured logging.**

## Running more than one instance

Multiple instances already work — the row locks exist precisely so concurrent
processes are safe against one database.

1. **The global bottleneck is already gone** (decision 1). What remains is a
   hot row per user, which only matters for one very active account. If it
   did, the counter could become an append-only table with the count derived.
2. **Pool sizing and lock timeouts**, so one stuck transaction can't hold
   product locks and stall a product indefinitely.
3. **Read replicas** for products, order reads and the report. Checkout stays
   on the primary because it needs the locks.
4. **Flash sales** turn one product row into a queue. The usual answer is a
   sharded reservation service, or a Redis counter reconciled against
   Postgres.
5. **Crash safety needs nothing extra.** An uncommitted transaction from a
   dead instance is rolled back by Postgres.

## How I used AI

I used an AI coding assistant throughout. I made the main design choices
first — row locks, the cart as the idempotency key, order snapshots, money as
integer paise — and used AI to turn those choices into code.

Four places where I did not go with what AI produced:

- **Postgres, not an in-memory store.** The brief allowed an in-memory
  implementation, and that is the quicker path. I picked Postgres because the
  rule I care most about — never sell the last item twice — is enforced by
  database row locks, and an in-memory map cannot demonstrate that.
- **Stock updates.** AI read the stock into Go, subtracted, and saved it
  back. Two checkouts at the same time can overwrite each other that way, and
  a sale goes missing. I replaced it with a single SQL statement that checks
  and subtracts at once.
- **Migrations in the binary.** AI bundled the SQL files in with `go:embed`.
  That does not even compile here, and it hides a file reviewers should be
  able to open. I kept plain `.sql` files instead.
- **A settings table.** AI stored `n` and `x` in a `reward_config` table.
  They are deployment settings, not customer data, so I read them from the
  environment and kept only what users earn in the database.

I did not take the locking on trust. I turned on statement logging and
confirmed the real `SELECT … FOR UPDATE` queries reach Postgres, and the
tests cover the rules that matter, including concurrent checkouts and
repeated webhooks.

## What I'd look at first with another two hours

1. **Expire stale pending orders**, closing the one hole decision 6 opens.
2. **Verify the webhook signature.** Right now anyone who guesses a
   `provider_ref` can settle a payment. It is the most serious gap.
3. **Load-test checkout over HTTP**, not just the repository, to get a real
   throughput number and confirm the per-user counter is no longer the limit.
4. **Move tests to a throwaway database**, so a run can never touch anything
   shared.
5. **Add Prometheus metrics and a Grafana dashboard**, so the concurrency
   behaviour is observable in a running system and not only in tests.
