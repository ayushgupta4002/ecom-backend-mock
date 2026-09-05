package repository_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ayush/checkout-rewards/internal/domain"
)

// Seeded product ids (see seed/seed.sql).
const (
	mouseID    int64 = 1
	keyboardID int64 = 2
	hubID      int64 = 3
	webcamID   int64 = 5 // limited inventory: 3
)

func mustAppErrCode(t *testing.T, err error, want domain.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %s, got nil", want)
	}
	var appErr *domain.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("expected domain.Error, got %T: %v", err, err)
	}
	if appErr.Code != want {
		t.Fatalf("expected code %s, got %s (%v)", want, appErr.Code, err)
	}
}

func TestAddItem_RejectsInvalidProductAndQuantity(t *testing.T) {
	repo, _ := setupRepo(t)
	ctx := context.Background()

	cart, err := repo.CreateCart(ctx, aaravID)
	if err != nil {
		t.Fatalf("create cart: %v", err)
	}

	err = repo.AddItem(ctx, cart.ID, 999, 1) // no such product
	mustAppErrCode(t, err, domain.CodeValidation)

	err = repo.AddItem(ctx, cart.ID, mouseID, 0)
	mustAppErrCode(t, err, domain.CodeValidation)

	err = repo.AddItem(ctx, cart.ID, mouseID, -3)
	mustAppErrCode(t, err, domain.CodeValidation)

	// Requesting more than available inventory must be rejected up front.
	err = repo.AddItem(ctx, cart.ID, webcamID, 999)
	mustAppErrCode(t, err, domain.CodeInsufficientStock)

	view, _ := repo.GetCartView(ctx, cart.ID)
	if len(view.Cart.Items) != 0 {
		t.Fatalf("invalid additions must not silently enter the cart, got %+v", view.Cart.Items)
	}
}

func TestCart_CannotBeModifiedOrCheckedOutTwiceAfterCheckout(t *testing.T) {
	repo, _ := setupRepo(t)
	ctx := context.Background()

	cart, _ := repo.CreateCart(ctx, aaravID)
	if err := repo.AddItem(ctx, cart.ID, mouseID, 1); err != nil {
		t.Fatalf("add item: %v", err)
	}
	if _, err := repo.Checkout(ctx, cart.ID, nil); err != nil {
		t.Fatalf("checkout: %v", err)
	}

	err := repo.AddItem(ctx, cart.ID, keyboardID, 1)
	mustAppErrCode(t, err, domain.CodeCartAlreadyCheckedOut)
}
