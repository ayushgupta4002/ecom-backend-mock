package repository_test

import (
	"context"
	"testing"
)

func TestReport_ReconcilesWithOrdersAndCoupons(t *testing.T) {
	repo, _ := setupRepo(t)
	ctx := context.Background()

	placeOrders(t, repo, aaravID, testMilestoneEveryN) // n orders of 1 mouse each, no discount
	coupon := couponsOf(t, repo, aaravID)[0]

	cart, _ := repo.CreateCart(ctx, aaravID)
	if err := repo.AddItem(ctx, cart.ID, keyboardID, 1); err != nil { // Rs 4,499.00, discounted
		t.Fatalf("add item: %v", err)
	}
	checkoutAndPay(t, repo, cart.ID, &coupon.Code)

	report1, err := repo.GetReport(ctx)
	if err != nil {
		t.Fatalf("get report: %v", err)
	}

	wantGross := int64(testMilestoneEveryN*89900 + 449900)
	wantDiscount := int64(449900 * testDiscountPercent / 100)
	wantNet := wantGross - wantDiscount

	if report1.GrossRevenuePaise != wantGross {
		t.Fatalf("expected gross %d, got %d", wantGross, report1.GrossRevenuePaise)
	}
	if report1.TotalDiscountPaise != wantDiscount {
		t.Fatalf("expected discount %d, got %d", wantDiscount, report1.TotalDiscountPaise)
	}
	if report1.NetRevenuePaise != wantNet {
		t.Fatalf("expected net %d, got %d", wantNet, report1.NetRevenuePaise)
	}
	wantOrders := int64(testMilestoneEveryN + 1) // n plain orders + 1 discounted order
	if report1.TotalOrders != wantOrders {
		t.Fatalf("expected %d total orders, got %d", wantOrders, report1.TotalOrders)
	}
	if report1.CouponsGenerated != 1 || report1.CouponsRedeemed != 1 || report1.CouponsAvailable != 0 {
		t.Fatalf("unexpected coupon counts: %+v", report1)
	}

	var mouseQty int64
	for _, p := range report1.ByProduct {
		if p.ProductID == mouseID {
			mouseQty = p.QuantitySold
		}
	}
	if mouseQty != int64(testMilestoneEveryN) {
		t.Fatalf("expected %d mice sold, got %d", testMilestoneEveryN, mouseQty)
	}

	// Repeated report requests must not mutate state.
	report2, err := repo.GetReport(ctx)
	if err != nil {
		t.Fatalf("get report again: %v", err)
	}
	if report1.GrossRevenuePaise != report2.GrossRevenuePaise ||
		report1.NetRevenuePaise != report2.NetRevenuePaise ||
		report1.TotalOrders != report2.TotalOrders {
		t.Fatalf("repeated report call produced different totals: %+v vs %+v", report1, report2)
	}
}
