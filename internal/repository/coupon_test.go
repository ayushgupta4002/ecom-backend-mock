package repository_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ayush/checkout-rewards/internal/domain"
	"github.com/ayush/checkout-rewards/internal/repository"
)

// placeOrders checks out n fresh single-item carts, driving the successful
// order counter forward by n.
func placeOrders(t *testing.T, repo *repository.Repo, userID int64, n int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		cart, err := repo.CreateCart(ctx, userID)
		if err != nil {
			t.Fatalf("create cart: %v", err)
		}
		if err := repo.AddItem(ctx, cart.ID, mouseID, 1); err != nil {
			t.Fatalf("add item: %v", err)
		}
		checkoutAndPay(t, repo, cart.ID, nil)
	}
}

// couponsOf returns every coupon a user currently holds.
func couponsOf(t *testing.T, repo *repository.Repo, userID int64) []domain.Coupon {
	t.Helper()
	coupons, err := repo.ListCouponsForUser(context.Background(), userID)
	if err != nil {
		t.Fatalf("list coupons: %v", err)
	}
	return coupons
}

// Coupons appear on their own the moment a payment completes a milestone --
// there is no manual generation step.
func TestCoupon_MintedAutomaticallyAtMilestone(t *testing.T) {
	repo, _ := setupRepo(t)

	// One short of the milestone: nothing yet.
	placeOrders(t, repo, aaravID, testMilestoneEveryN-1)
	if got := couponsOf(t, repo, aaravID); len(got) != 0 {
		t.Fatalf("expected no coupon before the milestone, got %d", len(got))
	}

	// The nth paid order mints it.
	placeOrders(t, repo, aaravID, 1)
	coupons := couponsOf(t, repo, aaravID)
	if len(coupons) != 1 {
		t.Fatalf("expected exactly 1 coupon at the milestone, got %d", len(coupons))
	}
	if coupons[0].MilestoneNumber == nil || *coupons[0].MilestoneNumber != 1 ||
		coupons[0].Status != domain.CouponAvailable {
		t.Fatalf("unexpected coupon: %+v", coupons[0])
	}
	if coupons[0].UserID == nil || *coupons[0].UserID != aaravID {
		t.Fatalf("coupon should belong to aarav, got %v", coupons[0].UserID)
	}
	if coupons[0].Source != domain.CouponFromMilestone || coupons[0].Global {
		t.Fatalf("expected an owned milestone coupon, got %+v", coupons[0])
	}

	// Orders in between mint nothing; the next milestone does.
	placeOrders(t, repo, aaravID, testMilestoneEveryN-1)
	if got := couponsOf(t, repo, aaravID); len(got) != 1 {
		t.Fatalf("expected still 1 coupon between milestones, got %d", len(got))
	}
	placeOrders(t, repo, aaravID, 1)
	coupons = couponsOf(t, repo, aaravID)
	if len(coupons) != 2 || coupons[1].MilestoneNumber == nil || *coupons[1].MilestoneNumber != 2 {
		t.Fatalf("expected a second coupon for milestone 2, got %+v", coupons)
	}
}

func TestCoupon_AppliedAtCheckoutAndCannotBeReused(t *testing.T) {
	repo, _ := setupRepo(t)
	ctx := context.Background()

	placeOrders(t, repo, aaravID, testMilestoneEveryN)
	coupon := couponsOf(t, repo, aaravID)[0]

	cart, _ := repo.CreateCart(ctx, aaravID)
	if err := repo.AddItem(ctx, cart.ID, mouseID, 1); err != nil { // Rs 899.00
		t.Fatalf("add item: %v", err)
	}
	order := checkoutAndPay(t, repo, cart.ID, &coupon.Code)
	wantDiscount := int64(89900 * testDiscountPercent / 100)
	if order.DiscountPaise != wantDiscount {
		t.Fatalf("expected discount %d, got %d", wantDiscount, order.DiscountPaise)
	}
	if order.TotalPaise != 89900-wantDiscount {
		t.Fatalf("expected total %d, got %d", 89900-wantDiscount, order.TotalPaise)
	}
	if order.TotalPaise < 0 {
		t.Fatalf("total must never be negative, got %d", order.TotalPaise)
	}

	cart2, _ := repo.CreateCart(ctx, aaravID)
	if err := repo.AddItem(ctx, cart2.ID, mouseID, 1); err != nil {
		t.Fatalf("add item: %v", err)
	}
	_, err := repo.Checkout(ctx, cart2.ID, &coupon.Code)
	mustAppErrCode(t, err, domain.CodeCouponAlreadyRedeemed)
}

// TestCoupon_NotConsumedByFailedCheckout verifies that a coupon supplied to
// a checkout which ultimately fails (e.g. due to insufficient stock)
// remains available for a later, successful checkout -- the whole checkout
// runs in one transaction, so a failure rolls back the coupon redemption
// along with everything else.
func TestCoupon_NotConsumedByFailedCheckout(t *testing.T) {
	repo, _ := setupRepo(t)
	ctx := context.Background()

	placeOrders(t, repo, aaravID, testMilestoneEveryN)
	coupon := couponsOf(t, repo, aaravID)[0]

	failingCart, _ := repo.CreateCart(ctx, aaravID)
	if err := repo.AddItem(ctx, failingCart.ID, webcamID, 3); err != nil {
		t.Fatalf("add item: %v", err)
	}
	// Drain the webcam stock elsewhere so failingCart's checkout fails.
	drainCart, _ := repo.CreateCart(ctx, aaravID)
	if err := repo.AddItem(ctx, drainCart.ID, webcamID, 3); err != nil {
		t.Fatalf("add item: %v", err)
	}
	if _, err := repo.Checkout(ctx, drainCart.ID, nil); err != nil {
		t.Fatalf("drain checkout: %v", err) // reserving is enough to consume stock
	}

	_, err := repo.Checkout(ctx, failingCart.ID, &coupon.Code)
	mustAppErrCode(t, err, domain.CodeInsufficientStock)

	reread, err2 := repo.GetCouponByCode(ctx, coupon.Code)
	if err2 != nil {
		t.Fatalf("get coupon: %v", err2)
	}
	if reread.Status != domain.CouponAvailable {
		t.Fatalf("coupon must remain available after a failed checkout, got status %s", reread.Status)
	}

	// And it can still be redeemed successfully afterwards.
	goodCart, _ := repo.CreateCart(ctx, aaravID)
	if err := repo.AddItem(ctx, goodCart.ID, mouseID, 1); err != nil {
		t.Fatalf("add item: %v", err)
	}
	checkoutAndPay(t, repo, goodCart.ID, &coupon.Code)
}

// TestCoupon_ConcurrentRedemptionOnlyOneWins runs two concurrent checkouts
// against different carts, both supplying the same coupon code. Exactly one
// must succeed with the discount applied; the other must fail cleanly.
func TestCoupon_ConcurrentRedemptionOnlyOneWins(t *testing.T) {
	repo, _ := setupRepo(t)
	ctx := context.Background()

	placeOrders(t, repo, aaravID, testMilestoneEveryN)
	coupon := couponsOf(t, repo, aaravID)[0]

	cartA, _ := repo.CreateCart(ctx, aaravID)
	if err := repo.AddItem(ctx, cartA.ID, mouseID, 1); err != nil {
		t.Fatalf("add item: %v", err)
	}
	cartB, _ := repo.CreateCart(ctx, aaravID)
	if err := repo.AddItem(ctx, cartB.ID, keyboardID, 1); err != nil {
		t.Fatalf("add item: %v", err)
	}

	var wg sync.WaitGroup
	var successes int64
	results := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, results[0] = repo.Checkout(ctx, cartA.ID, &coupon.Code)
		if results[0] == nil {
			atomic.AddInt64(&successes, 1)
		}
	}()
	go func() {
		defer wg.Done()
		_, results[1] = repo.Checkout(ctx, cartB.ID, &coupon.Code)
		if results[1] == nil {
			atomic.AddInt64(&successes, 1)
		}
	}()
	wg.Wait()

	if successes != 1 {
		t.Fatalf("expected exactly one checkout to win the coupon, got %d successes (errA=%v errB=%v)", successes, results[0], results[1])
	}
	for _, err := range results {
		if err != nil {
			mustAppErrCode(t, err, domain.CodeCouponAlreadyRedeemed)
		}
	}

	final, err := repo.GetCouponByCode(ctx, coupon.Code)
	if err != nil {
		t.Fatalf("get coupon: %v", err)
	}
	if final.Status != domain.CouponRedeemed {
		t.Fatalf("expected coupon to be redeemed, got %s", final.Status)
	}
}

// Concurrent settlements for one user must not mint two coupons for the
// same milestone. The user row lock serialises the counter, and
// UNIQUE (user_id, milestone_number) is the backstop.
func TestCoupon_ConcurrentSettlementsMintOnce(t *testing.T) {
	repo, gormDB := setupRepo(t)
	ctx := context.Background()

	placeOrders(t, repo, aaravID, testMilestoneEveryN-1)

	// Open several pending orders, then settle them all at once. Exactly one
	// of them is this user's nth paid order.
	const settlements = 6
	refs := make([]string, settlements)
	for i := 0; i < settlements; i++ {
		refs[i] = pendingOrder(t, repo, aaravID, mouseID).Payment.ProviderRef
	}

	var wg sync.WaitGroup
	for _, ref := range refs {
		wg.Add(1)
		go func(ref string) {
			defer wg.Done()
			if _, err := repo.SettlePayment(ctx, ref, true); err != nil {
				t.Errorf("settle: %v", err)
			}
		}(ref)
	}
	wg.Wait()

	var milestone1 int64
	if err := gormDB.Table("coupons").
		Where("user_id = ? AND milestone_number = 1", aaravID).
		Count(&milestone1).Error; err != nil {
		t.Fatalf("count coupons: %v", err)
	}
	if milestone1 != 1 {
		t.Fatalf("expected exactly 1 coupon for milestone 1, got %d", milestone1)
	}
}

// TestMilestone_IsPerUser verifies that one user's purchases do not earn
// another user a coupon: Aarav places n orders and becomes eligible, while
// Priya (with fewer orders) does not, even though the store total is well
// past n.
func TestMilestone_IsPerUser(t *testing.T) {
	repo, _ := setupRepo(t)

	placeOrders(t, repo, aaravID, testMilestoneEveryN)
	placeOrders(t, repo, priyaID, testMilestoneEveryN-2)

	// Aarav reached his own milestone, so he has a coupon.
	if got := couponsOf(t, repo, aaravID); len(got) != 1 || got[0].UserID == nil || *got[0].UserID != aaravID {
		t.Fatalf("expected aarav to hold 1 coupon, got %+v", got)
	}

	// Priya has not, despite the store being past the milestone overall.
	if got := couponsOf(t, repo, priyaID); len(got) != 0 {
		t.Fatalf("priya has not reached her own milestone, got %+v", got)
	}
	// A user with no orders at all likewise has nothing.
	if got := couponsOf(t, repo, rohanID); len(got) != 0 {
		t.Fatalf("rohan has no orders, got %+v", got)
	}
}

// TestCoupon_CannotBeRedeemedByAnotherUser is the core user-level rule: a
// coupon Aarav earned is worthless in Priya's cart.
func TestCoupon_CannotBeRedeemedByAnotherUser(t *testing.T) {
	repo, _ := setupRepo(t)
	ctx := context.Background()

	placeOrders(t, repo, aaravID, testMilestoneEveryN)
	coupon := couponsOf(t, repo, aaravID)[0]

	priyaCart, _ := repo.CreateCart(ctx, priyaID)
	if err := repo.AddItem(ctx, priyaCart.ID, mouseID, 1); err != nil {
		t.Fatalf("add item: %v", err)
	}

	_, err := repo.Checkout(ctx, priyaCart.ID, &coupon.Code)
	mustAppErrCode(t, err, domain.CodeCouponNotOwned)

	// The rejected attempt must not have consumed Aarav's coupon.
	reread, err2 := repo.GetCouponByCode(ctx, coupon.Code)
	if err2 != nil {
		t.Fatalf("get coupon: %v", err2)
	}
	if reread.Status != domain.CouponAvailable {
		t.Fatalf("coupon must remain available, got %s", reread.Status)
	}

	// And the rightful owner can still redeem it.
	aaravCart, _ := repo.CreateCart(ctx, aaravID)
	if err := repo.AddItem(ctx, aaravCart.ID, mouseID, 1); err != nil {
		t.Fatalf("add item: %v", err)
	}
	order := checkoutAndPay(t, repo, aaravCart.ID, &coupon.Code)
	if order.DiscountPaise == 0 {
		t.Fatal("expected owner's checkout to receive the discount")
	}
}

// An administrator can grant a coupon to one customer -- a goodwill gesture
// or a backfill. It behaves exactly like an earned one: owned, single-use.
func TestAdminCoupon_GrantedToOneUserIsOwned(t *testing.T) {
	repo, _ := setupRepo(t)
	ctx := context.Background()

	owner := aaravID
	pct := 25
	coupon, err := repo.CreateAdminCoupon(ctx, repository.AdminCouponRequest{
		UserID: &owner, DiscountPercent: &pct,
	})
	if err != nil {
		t.Fatalf("create admin coupon: %v", err)
	}
	if coupon.Global || coupon.Source != domain.CouponFromAdmin {
		t.Fatalf("expected an owned admin coupon, got %+v", coupon)
	}
	if coupon.MilestoneNumber != nil {
		t.Fatal("an admin coupon must not claim a milestone")
	}
	if coupon.DiscountPercent != pct {
		t.Fatalf("expected %d%%, got %d", pct, coupon.DiscountPercent)
	}

	// Priya cannot spend it.
	priyaCart, _ := repo.CreateCart(ctx, priyaID)
	if err := repo.AddItem(ctx, priyaCart.ID, mouseID, 1); err != nil {
		t.Fatalf("add item: %v", err)
	}
	_, err = repo.Checkout(ctx, priyaCart.ID, &coupon.Code)
	mustAppErrCode(t, err, domain.CodeCouponNotOwned)

	// Aarav can, at the granted rate.
	cart, _ := repo.CreateCart(ctx, aaravID)
	if err := repo.AddItem(ctx, cart.ID, mouseID, 1); err != nil {
		t.Fatalf("add item: %v", err)
	}
	order := checkoutAndPay(t, repo, cart.ID, &coupon.Code)
	if order.DiscountPaise != 89900*int64(pct)/100 {
		t.Fatalf("expected %d%% off 89900, got %d", pct, order.DiscountPaise)
	}
}

// A global coupon has no owner, so any customer may redeem it -- but it is
// still single-use, so the second customer is refused.
func TestAdminCoupon_GlobalIsRedeemableByAnyoneOnce(t *testing.T) {
	repo, _ := setupRepo(t)
	ctx := context.Background()

	coupon, err := repo.CreateAdminCoupon(ctx, repository.AdminCouponRequest{Code: "diwali10"})
	if err != nil {
		t.Fatalf("create global coupon: %v", err)
	}
	if !coupon.Global || coupon.UserID != nil {
		t.Fatalf("expected a global coupon, got %+v", coupon)
	}
	if coupon.Code != "DIWALI10" {
		t.Fatalf("expected the code to be normalised to upper case, got %q", coupon.Code)
	}

	// Priya, who has earned nothing, may still use it.
	priyaCart, _ := repo.CreateCart(ctx, priyaID)
	if err := repo.AddItem(ctx, priyaCart.ID, mouseID, 1); err != nil {
		t.Fatalf("add item: %v", err)
	}
	order := checkoutAndPay(t, repo, priyaCart.ID, &coupon.Code)
	if order.DiscountPaise == 0 {
		t.Fatal("expected the global coupon to apply")
	}

	// It is spent; Rohan cannot reuse it.
	rohanCart, _ := repo.CreateCart(ctx, rohanID)
	if err := repo.AddItem(ctx, rohanCart.ID, mouseID, 1); err != nil {
		t.Fatalf("add item: %v", err)
	}
	_, err = repo.Checkout(ctx, rohanCart.ID, &coupon.Code)
	mustAppErrCode(t, err, domain.CodeCouponAlreadyRedeemed)
}

// An admin grant must not be mistaken for reward progress.
func TestAdminCoupon_DoesNotAffectMilestoneProgress(t *testing.T) {
	repo, _ := setupRepo(t)
	ctx := context.Background()

	placeOrders(t, repo, aaravID, 2)
	owner := aaravID
	if _, err := repo.CreateAdminCoupon(ctx, repository.AdminCouponRequest{UserID: &owner}); err != nil {
		t.Fatalf("create: %v", err)
	}

	status, err := repo.GetRewardStatus(ctx, aaravID)
	if err != nil {
		t.Fatalf("reward status: %v", err)
	}
	if status.CouponsGenerated != 0 {
		t.Fatalf("admin grants are not milestone rewards, got %d", status.CouponsGenerated)
	}
	if status.OrdersUntilNext != int64(testMilestoneEveryN-2) {
		t.Fatalf("progress should be untouched, got %d", status.OrdersUntilNext)
	}
}
