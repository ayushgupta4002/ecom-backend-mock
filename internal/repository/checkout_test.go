package repository_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ayush/checkout-rewards/internal/domain"
	"gorm.io/gorm"
)

func TestCheckout_ComputesMoneyExactlyWithIntegerPaise(t *testing.T) {
	repo, _ := setupRepo(t)
	ctx := context.Background()

	cart, _ := repo.CreateCart(ctx, aaravID)
	if err := repo.AddItem(ctx, cart.ID, mouseID, 3); err != nil { // 3 * 89900
		t.Fatalf("add item: %v", err)
	}
	if err := repo.AddItem(ctx, cart.ID, hubID, 2); err != nil { // 2 * 199900
		t.Fatalf("add item: %v", err)
	}

	order := checkoutAndPay(t, repo, cart.ID, nil)
	wantSubtotal := int64(3*89900 + 2*199900)
	if order.SubtotalPaise != wantSubtotal {
		t.Fatalf("expected subtotal %d, got %d", wantSubtotal, order.SubtotalPaise)
	}
	if order.DiscountPaise != 0 || order.TotalPaise != wantSubtotal {
		t.Fatalf("expected no discount, got discount=%d total=%d", order.DiscountPaise, order.TotalPaise)
	}
	if order.UserOrderNumber == nil || *order.UserOrderNumber != 1 {
		t.Fatalf("expected this user's first paid order to be number 1, got %v", order.UserOrderNumber)
	}
}

// TestCheckout_RetryIsIdempotent simulates a client that times out waiting
// for the checkout response and retries: the retry must return the same
// order, not create a second one or decrement inventory again.
func TestCheckout_RetryIsIdempotent(t *testing.T) {
	repo, gormDB := setupRepo(t)
	ctx := context.Background()

	cart, _ := repo.CreateCart(ctx, aaravID)
	if err := repo.AddItem(ctx, cart.ID, webcamID, 1); err != nil {
		t.Fatalf("add item: %v", err)
	}

	first, err := repo.Checkout(ctx, cart.ID, nil)
	if err != nil {
		t.Fatalf("first checkout: %v", err)
	}

	retry, err := repo.Checkout(ctx, cart.ID, nil)
	if err != nil {
		t.Fatalf("retry checkout: %v", err)
	}
	if retry.ID != first.ID {
		t.Fatalf("retry created a different order: first=%d retry=%d", first.ID, retry.ID)
	}

	orderCount := countOrdersForCart(t, gormDB, cart.ID)
	if orderCount != 1 {
		t.Fatalf("expected exactly 1 order after retry, got %d", orderCount)
	}

	inventory := readInventory(t, gormDB, webcamID)
	if inventory != 2 { // started at 3, decremented once
		t.Fatalf("expected inventory 2 after single decrement, got %d", inventory)
	}
}

// TestCheckout_ConcurrentCheckoutsDoNotOversell hammers a product with only
// 3 units of stock from 10 concurrent carts each requesting 1 unit. At most
// 3 can succeed; the rest must fail with insufficient stock, and final
// inventory must never go negative.
//
// The carts are spread across different users so that what is being tested
// is contention on the product row, not on any one user's row.
func TestCheckout_ConcurrentCheckoutsDoNotOversell(t *testing.T) {
	repo, gormDB := setupRepo(t)
	ctx := context.Background()

	const attempts = 10
	users := []int64{aaravID, priyaID, rohanID}
	cartIDs := make([]int64, attempts)
	for i := 0; i < attempts; i++ {
		cart, err := repo.CreateCart(ctx, users[i%len(users)])
		if err != nil {
			t.Fatalf("create cart: %v", err)
		}
		if err := repo.AddItem(ctx, cart.ID, webcamID, 1); err != nil {
			t.Fatalf("add item: %v", err)
		}
		cartIDs[i] = cart.ID
	}

	var wg sync.WaitGroup
	var successes int64
	var failures int64
	for _, cartID := range cartIDs {
		wg.Add(1)
		go func(cartID int64) {
			defer wg.Done()
			_, err := repo.Checkout(ctx, cartID, nil)
			if err == nil {
				atomic.AddInt64(&successes, 1)
			} else {
				mustAppErrCode(t, err, domain.CodeInsufficientStock)
				atomic.AddInt64(&failures, 1)
			}
		}(cartID)
	}
	wg.Wait()

	if successes != 3 {
		t.Fatalf("expected exactly 3 successful checkouts (stock=3), got %d (failures=%d)", successes, failures)
	}

	inventory := readInventory(t, gormDB, webcamID)
	if inventory != 0 {
		t.Fatalf("expected inventory to reach exactly 0, got %d (oversold or undersold)", inventory)
	}
}

func readInventory(t *testing.T, gormDB *gorm.DB, productID int64) int {
	t.Helper()
	var inventory int
	err := gormDB.Table("products").Select("inventory").Where("id = ?", productID).Scan(&inventory).Error
	if err != nil {
		t.Fatalf("read inventory: %v", err)
	}
	return inventory
}

func countOrdersForCart(t *testing.T, gormDB *gorm.DB, cartID int64) int64 {
	t.Helper()
	var count int64
	if err := gormDB.Table("orders").Where("cart_id = ?", cartID).Count(&count).Error; err != nil {
		t.Fatalf("count orders: %v", err)
	}
	return count
}
