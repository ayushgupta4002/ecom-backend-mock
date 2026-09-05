package repository

import (
	"context"
	"errors"

	"github.com/ayush/checkout-rewards/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CreateCart opens a cart owned by userID. Authentication is out of scope,
// so the caller asserts the user; in production this would come from the
// authenticated session rather than the request body.
func (r *Repo) CreateCart(ctx context.Context, userID int64) (domain.Cart, error) {
	if userID <= 0 {
		return domain.Cart{}, domain.NewError(domain.CodeValidation, "a valid user_id is required")
	}

	var exists int64
	if err := r.db.WithContext(ctx).Model(&User{}).Where("id = ?", userID).Count(&exists).Error; err != nil {
		return domain.Cart{}, domain.WrapError(domain.CodeInternal, "check user", err)
	}
	if exists == 0 {
		return domain.Cart{}, domain.NewError(domain.CodeValidation, "user does not exist")
	}

	cart := Cart{UserID: userID, Status: string(domain.CartOpen)}
	if err := r.db.WithContext(ctx).Create(&cart).Error; err != nil {
		return domain.Cart{}, domain.WrapError(domain.CodeInternal, "create cart", err)
	}
	return domain.Cart{
		ID:        cart.ID,
		UserID:    cart.UserID,
		Status:    domain.CartStatus(cart.Status),
		Items:     []domain.CartItem{},
		CreatedAt: cart.CreatedAt,
		UpdatedAt: cart.UpdatedAt,
	}, nil
}

// GetCartView returns the cart joined with *current* product data. This is
// informational: prices/availability shown here can still drift before the
// client checks out, at which point checkout re-validates authoritatively.
func (r *Repo) GetCartView(ctx context.Context, cartID int64) (domain.CartView, error) {
	var cart Cart
	err := r.db.WithContext(ctx).Where("id = ?", cartID).First(&cart).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.CartView{}, domain.NewError(domain.CodeNotFound, "cart not found")
	}
	if err != nil {
		return domain.CartView{}, domain.WrapError(domain.CodeInternal, "get cart", err)
	}

	type joinedRow struct {
		ID          int64
		ProductID   int64
		ProductName string
		Quantity    int
		PricePaise  int64
		Inventory   int
	}
	var rows []joinedRow
	err = r.db.WithContext(ctx).
		Table("cart_items AS ci").
		Select("ci.id, ci.product_id, p.name AS product_name, ci.quantity, p.price_paise, p.inventory").
		Joins("JOIN products p ON p.id = ci.product_id").
		Where("ci.cart_id = ?", cartID).
		Order("p.name").
		Scan(&rows).Error
	if err != nil {
		return domain.CartView{}, domain.WrapError(domain.CodeInternal, "list cart items", err)
	}

	items := make([]domain.CartItem, 0, len(rows))
	var subtotal int64
	for _, row := range rows {
		lineTotal := row.PricePaise * int64(row.Quantity)
		subtotal += lineTotal
		items = append(items, domain.CartItem{
			ID:                    row.ID,
			ProductID:             row.ProductID,
			ProductName:           row.ProductName,
			Quantity:              row.Quantity,
			CurrentUnitPricePaise: row.PricePaise,
			LineTotalPaise:        lineTotal,
			AvailableInStock:      row.Inventory,
		})
	}

	return domain.CartView{
		Cart: domain.Cart{
			ID:        cart.ID,
			UserID:    cart.UserID,
			Status:    domain.CartStatus(cart.Status),
			Items:     items,
			CreatedAt: cart.CreatedAt,
			UpdatedAt: cart.UpdatedAt,
		},
		SubtotalPaise: subtotal,
	}, nil
}

// AddItem adds a product to a cart. If the product is already present in
// the cart, this returns a conflict error instructing the client to use
// UpdateItemQuantity instead -- an explicit, unambiguous cart-building API
// (see DECISIONS.md).
func (r *Repo) AddItem(ctx context.Context, cartID, productID int64, quantity int) error {
	if quantity <= 0 {
		return domain.NewError(domain.CodeValidation, "quantity must be positive")
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockOpenCart(tx, cartID); err != nil {
			return err
		}

		if err := checkProductAvailable(tx, productID, quantity); err != nil {
			return err
		}

		// ON CONFLICT DO NOTHING: if the product is already in this cart the
		// insert affects no rows, which we surface as a conflict rather than
		// silently changing the existing quantity.
		item := CartItem{
			CartID:    cartID,
			ProductID: productID,
			Quantity:  quantity,
		}
		res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&item)
		if res.Error != nil {
			return domain.WrapError(domain.CodeInternal, "insert cart item", res.Error)
		}
		if res.RowsAffected == 0 {
			return domain.NewError(domain.CodeConflict, "product already in cart; update its quantity instead")
		}

		return touchCart(tx, cartID)
	})
}

// UpdateItemQuantity sets the exact quantity of an existing cart item.
func (r *Repo) UpdateItemQuantity(ctx context.Context, cartID, productID int64, quantity int) error {
	if quantity <= 0 {
		return domain.NewError(domain.CodeValidation, "quantity must be positive; use remove to delete an item")
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockOpenCart(tx, cartID); err != nil {
			return err
		}

		if err := checkProductAvailable(tx, productID, quantity); err != nil {
			return err
		}

		res := tx.Model(&CartItem{}).
			Where("cart_id = ? AND product_id = ?", cartID, productID).
			Update("quantity", quantity)
		if res.Error != nil {
			return domain.WrapError(domain.CodeInternal, "update cart item", res.Error)
		}
		if res.RowsAffected == 0 {
			return domain.NewError(domain.CodeNotFound, "item not in cart")
		}

		return touchCart(tx, cartID)
	})
}

func (r *Repo) RemoveItem(ctx context.Context, cartID, productID int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockOpenCart(tx, cartID); err != nil {
			return err
		}

		res := tx.Where("cart_id = ? AND product_id = ?", cartID, productID).Delete(&CartItem{})
		if res.Error != nil {
			return domain.WrapError(domain.CodeInternal, "remove cart item", res.Error)
		}
		if res.RowsAffected == 0 {
			return domain.NewError(domain.CodeNotFound, "item not in cart")
		}

		return touchCart(tx, cartID)
	})
}

// lockOpenCart takes an exclusive lock on the cart row and ensures the cart
// exists and is still open. Must be called inside a transaction.
func lockOpenCart(tx *gorm.DB, cartID int64) error {
	var cart Cart
	err := forUpdate(tx).Where("id = ?", cartID).First(&cart).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.NewError(domain.CodeNotFound, "cart not found")
	}
	if err != nil {
		return domain.WrapError(domain.CodeInternal, "lock cart", err)
	}
	if cart.Status != string(domain.CartOpen) {
		return domain.NewError(domain.CodeCartAlreadyCheckedOut,
			"cart has already been checked out and can no longer be modified")
	}
	return nil
}

// checkProductAvailable validates that the product exists and currently has
// enough stock, WITHOUT locking it. Cart edits only need friendly up-front
// feedback; nothing is reserved here and the authoritative check happens
// under a row lock at checkout.
func checkProductAvailable(tx *gorm.DB, productID int64, quantity int) error {
	var product Product
	err := tx.Where("id = ?", productID).First(&product).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.NewError(domain.CodeValidation, "product does not exist")
	}
	if err != nil {
		return domain.WrapError(domain.CodeInternal, "check product", err)
	}
	if product.Inventory < quantity {
		return domain.NewError(domain.CodeInsufficientStock, "not enough inventory available for this product")
	}
	return nil
}

func touchCart(tx *gorm.DB, cartID int64) error {
	if err := tx.Model(&Cart{}).Where("id = ?", cartID).
		Update("updated_at", gorm.Expr("now()")).Error; err != nil {
		return domain.WrapError(domain.CodeInternal, "touch cart", err)
	}
	return nil
}
