package repository

import (
	"context"
	"errors"

	"github.com/ayush/checkout-rewards/internal/domain"
	"gorm.io/gorm"
)

func (r *Repo) ListProducts(ctx context.Context) ([]domain.Product, error) {
	var rows []Product
	if err := r.db.WithContext(ctx).Order("name").Find(&rows).Error; err != nil {
		return nil, domain.WrapError(domain.CodeInternal, "list products", err)
	}

	out := make([]domain.Product, 0, len(rows))
	for _, p := range rows {
		out = append(out, toDomainProduct(p))
	}
	return out, nil
}

func (r *Repo) GetProduct(ctx context.Context, id int64) (domain.Product, error) {
	var p Product
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.Product{}, domain.NewError(domain.CodeNotFound, "product not found")
	}
	if err != nil {
		return domain.Product{}, domain.WrapError(domain.CodeInternal, "get product", err)
	}
	return toDomainProduct(p), nil
}

func toDomainProduct(p Product) domain.Product {
	return domain.Product{
		ID:         p.ID,
		Name:       p.Name,
		PricePaise: p.PricePaise,
		Inventory:  p.Inventory,
		CreatedAt:  p.CreatedAt,
		UpdatedAt:  p.UpdatedAt,
	}
}
