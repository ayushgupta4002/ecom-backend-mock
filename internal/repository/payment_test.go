package repository_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ayush/checkout-rewards/internal/domain"
	"github.com/ayush/checkout-rewards/internal/repository"
)

// pendingOrder opens a cart with one webcam and checks out, leaving a
// pending order with stock reserved.
func pendingOrder(t *testing.T, repo *repository.Repo, userID, productID int64) domain.Order {
	t.Helper()
	ctx := context.Background()

	cart, err := repo.CreateCart(ctx, userID)
	if err != nil {
		t.Fatalf("create cart: %v", err)
	}
	if err := repo.AddItem(ctx, cart.ID, productID, 1); err != nil {
		t.Fatalf("add item: %v", err)
	}
	order, err := repo.Checkout(ctx, cart.ID, nil)
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	return order
}

// Checkout must reserve stock immediately, not wait for payment. Otherwise
// everyone could pay for the last item and all but one would need a refund.
func TestCheckout_ReservesStockBeforePayment(t *testing.T) {
	repo, gormDB := setupRepo(t)

	order := pendingOrder(t, repo, aaravID, webcamID)

	if order.Status != domain.OrderPending {
		t.Fatalf("expected pending, got %s", order.Status)
	}
	if order.Payment == nil || order.Payment.Status != domain.PaymentInitiated {
		t.Fatalf("expected an initiated payment, got %+v", order.Payment)
	}
	if order.UserOrderNumber != nil {
		t.Fatalf("an unpaid order must not hold a reward position, got %v", *order.UserOrderNumber)
	}
	if got := readInventory(t, gormDB, webcamID); got != 2 {
		t.Fatalf("expected stock reserved (3 -> 2), got %d", got)
	}
}

// Only a paid order counts toward rewards.
func TestPayment_SuccessMarksOrderPaidAndAdvancesRewards(t *testing.T) {
	repo, _ := setupRepo(t)
	ctx := context.Background()

	pending := pendingOrder(t, repo, aaravID, mouseID)

	before, _ := repo.GetRewardStatus(ctx, aaravID)
	if before.SuccessfulOrderCount != 0 {
		t.Fatalf("a pending order must not count, got %d", before.SuccessfulOrderCount)
	}

	paid, err := repo.SettlePayment(ctx, pending.Payment.ProviderRef, true)
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if paid.Status != domain.OrderPaid || paid.Payment.Status != domain.PaymentSuccess {
		t.Fatalf("expected paid order and successful payment, got %s / %s", paid.Status, paid.Payment.Status)
	}
	if paid.UserOrderNumber == nil || *paid.UserOrderNumber != 1 {
		t.Fatalf("expected reward position 1, got %v", paid.UserOrderNumber)
	}
	if paid.PaidAt == nil {
		t.Fatal("expected paid_at to be set")
	}

	after, _ := repo.GetRewardStatus(ctx, aaravID)
	if after.SuccessfulOrderCount != 1 {
		t.Fatalf("expected 1 successful order after payment, got %d", after.SuccessfulOrderCount)
	}
}

// A failed payment must release everything the order was holding.
func TestPayment_FailureReleasesStockAndCoupon(t *testing.T) {
	repo, gormDB := setupRepo(t)
	ctx := context.Background()

	// Earn a coupon first.
	placeOrders(t, repo, aaravID, testMilestoneEveryN)
	coupon := couponsOf(t, repo, aaravID)[0]

	cart, _ := repo.CreateCart(ctx, aaravID)
	if err := repo.AddItem(ctx, cart.ID, webcamID, 2); err != nil {
		t.Fatalf("add item: %v", err)
	}
	pending, err := repo.Checkout(ctx, cart.ID, &coupon.Code)
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if got := readInventory(t, gormDB, webcamID); got != 1 {
		t.Fatalf("expected 2 reserved (3 -> 1), got %d", got)
	}

	failed, err := repo.SettlePayment(ctx, pending.Payment.ProviderRef, false)
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if failed.Status != domain.OrderFailed || failed.Payment.Status != domain.PaymentFailed {
		t.Fatalf("expected failed order and payment, got %s / %s", failed.Status, failed.Payment.Status)
	}

	if got := readInventory(t, gormDB, webcamID); got != 3 {
		t.Fatalf("stock must be released back to 3, got %d", got)
	}
	reread, _ := repo.GetCouponByCode(ctx, coupon.Code)
	if reread.Status != domain.CouponAvailable {
		t.Fatalf("coupon must return to available, got %s", reread.Status)
	}
	// And it is genuinely reusable.
	cart2, _ := repo.CreateCart(ctx, aaravID)
	if err := repo.AddItem(ctx, cart2.ID, mouseID, 1); err != nil {
		t.Fatalf("add item: %v", err)
	}
	if order := checkoutAndPay(t, repo, cart2.ID, &coupon.Code); order.DiscountPaise == 0 {
		t.Fatal("expected the released coupon to apply on a later order")
	}

	// A failed order never counted toward rewards.
	status, _ := repo.GetRewardStatus(ctx, aaravID)
	if status.SuccessfulOrderCount != int64(testMilestoneEveryN)+1 {
		t.Fatalf("expected %d successful orders, got %d", testMilestoneEveryN+1, status.SuccessfulOrderCount)
	}
}

// Providers retry webhooks. A redelivery must not apply twice.
func TestPayment_WebhookIsIdempotent(t *testing.T) {
	repo, gormDB := setupRepo(t)
	ctx := context.Background()

	pending := pendingOrder(t, repo, aaravID, webcamID)

	first, err := repo.SettlePayment(ctx, pending.Payment.ProviderRef, true)
	if err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	replay, err := repo.SettlePayment(ctx, pending.Payment.ProviderRef, true)
	if err != nil {
		t.Fatalf("redelivery: %v", err)
	}

	if *replay.UserOrderNumber != *first.UserOrderNumber {
		t.Fatalf("redelivery changed the reward position: %d -> %d",
			*first.UserOrderNumber, *replay.UserOrderNumber)
	}
	status, _ := repo.GetRewardStatus(ctx, aaravID)
	if status.SuccessfulOrderCount != 1 {
		t.Fatalf("redelivery double-counted rewards: %d", status.SuccessfulOrderCount)
	}
	if got := readInventory(t, gormDB, webcamID); got != 2 {
		t.Fatalf("redelivery touched stock, got %d", got)
	}

	// A late "failed" delivery after success must not reverse a paid order.
	late, err := repo.SettlePayment(ctx, pending.Payment.ProviderRef, false)
	if err != nil {
		t.Fatalf("late delivery: %v", err)
	}
	if late.Status != domain.OrderPaid {
		t.Fatalf("a settled payment must not be re-settled, got %s", late.Status)
	}
}

// Concurrent deliveries of the same webhook: exactly one may take effect.
func TestPayment_ConcurrentWebhookDeliveriesApplyOnce(t *testing.T) {
	repo, _ := setupRepo(t)
	ctx := context.Background()

	pending := pendingOrder(t, repo, aaravID, mouseID)

	const deliveries = 8
	var wg sync.WaitGroup
	var errs int64
	for i := 0; i < deliveries; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := repo.SettlePayment(ctx, pending.Payment.ProviderRef, true); err != nil {
				atomic.AddInt64(&errs, 1)
			}
		}()
	}
	wg.Wait()

	if errs != 0 {
		t.Fatalf("expected every delivery to succeed (idempotently), got %d errors", errs)
	}
	status, _ := repo.GetRewardStatus(ctx, aaravID)
	if status.SuccessfulOrderCount != 1 {
		t.Fatalf("expected exactly 1 counted order across %d deliveries, got %d",
			deliveries, status.SuccessfulOrderCount)
	}
}

func TestCancel_PendingOrderReleasesStockAndCoupon(t *testing.T) {
	repo, gormDB := setupRepo(t)
	ctx := context.Background()

	pending := pendingOrder(t, repo, aaravID, webcamID)
	if got := readInventory(t, gormDB, webcamID); got != 2 {
		t.Fatalf("expected reserved, got %d", got)
	}

	cancelled, err := repo.CancelOrder(ctx, pending.ID)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cancelled.Status != domain.OrderCancelled || cancelled.CancelledAt == nil {
		t.Fatalf("expected cancelled order, got %+v", cancelled.Status)
	}
	if got := readInventory(t, gormDB, webcamID); got != 3 {
		t.Fatalf("cancelling must release stock, got %d", got)
	}

	status, _ := repo.GetRewardStatus(ctx, aaravID)
	if status.SuccessfulOrderCount != 0 {
		t.Fatalf("a cancelled order must not count, got %d", status.SuccessfulOrderCount)
	}
}

// Refunding a paid order restocks, but deliberately does not revoke rewards
// already earned -- that would strand a coupon the user may already hold.
func TestCancel_PaidOrderRefundsStockButKeepsRewards(t *testing.T) {
	repo, gormDB := setupRepo(t)
	ctx := context.Background()

	pending := pendingOrder(t, repo, aaravID, webcamID)
	paid, err := repo.SettlePayment(ctx, pending.Payment.ProviderRef, true)
	if err != nil {
		t.Fatalf("settle: %v", err)
	}

	if _, err := repo.CancelOrder(ctx, paid.ID); err != nil {
		t.Fatalf("cancel paid: %v", err)
	}

	if got := readInventory(t, gormDB, webcamID); got != 3 {
		t.Fatalf("refund must restock, got %d", got)
	}
	status, _ := repo.GetRewardStatus(ctx, aaravID)
	if status.SuccessfulOrderCount != 1 {
		t.Fatalf("rewards already earned must stand, got %d", status.SuccessfulOrderCount)
	}
}

// The report reflects money actually taken: pending, failed and cancelled
// orders must not appear in it.
func TestReport_CountsOnlyPaidOrders(t *testing.T) {
	repo, _ := setupRepo(t)
	ctx := context.Background()

	// One paid, one left pending, one failed.
	cart, _ := repo.CreateCart(ctx, aaravID)
	if err := repo.AddItem(ctx, cart.ID, mouseID, 1); err != nil {
		t.Fatalf("add item: %v", err)
	}
	checkoutAndPay(t, repo, cart.ID, nil)

	pendingOrder(t, repo, priyaID, mouseID)

	toFail := pendingOrder(t, repo, rohanID, mouseID)
	if _, err := repo.SettlePayment(ctx, toFail.Payment.ProviderRef, false); err != nil {
		t.Fatalf("fail payment: %v", err)
	}

	report, err := repo.GetReport(ctx)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if report.TotalOrders != 1 {
		t.Fatalf("expected only the paid order counted, got %d", report.TotalOrders)
	}
	if report.GrossRevenuePaise != 89900 {
		t.Fatalf("expected gross 89900, got %d", report.GrossRevenuePaise)
	}
	var mouseQty int64
	for _, p := range report.ByProduct {
		if p.ProductID == mouseID {
			mouseQty = p.QuantitySold
		}
	}
	if mouseQty != 1 {
		t.Fatalf("expected 1 mouse counted as sold, got %d", mouseQty)
	}
}
