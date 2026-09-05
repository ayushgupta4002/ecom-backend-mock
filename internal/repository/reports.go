package repository

import (
	"context"

	"github.com/ayush/checkout-rewards/internal/domain"
)

// GetReport reads orders, order_items and coupons only. Nothing is written,
// so repeated calls are safe and the numbers always reconcile.
//
// Only PAID orders count. Pending orders have not been paid for, and
// failed/cancelled ones have had their stock and coupons released, so
// including any of them would overstate both quantity sold and revenue.
func (r *Repo) GetReport(ctx context.Context) (domain.Report, error) {
	db := r.db.WithContext(ctx)
	var rep domain.Report

	// Quantity and gross revenue per product, from the snapshotted line
	// items rather than live product data, so history stays accurate.
	rows := []domain.ProductRevenue{}
	err := db.Table("order_items AS oi").
		Select("oi.product_id, oi.product_name, SUM(oi.quantity) AS quantity_sold, SUM(oi.line_total_paise) AS gross_revenue_paise").
		Joins("JOIN orders o ON o.id = oi.order_id").
		Where("o.status = ?", string(domain.OrderPaid)).
		Group("oi.product_id, oi.product_name").
		Order("oi.product_name").
		Scan(&rows).Error
	if err != nil {
		return domain.Report{}, domain.WrapError(domain.CodeInternal, "report by product", err)
	}
	rep.ByProduct = rows

	var totals struct {
		GrossRevenuePaise  int64
		TotalDiscountPaise int64
		NetRevenuePaise    int64
		TotalOrders        int64
	}
	err = db.Model(&Order{}).
		Where("status = ?", string(domain.OrderPaid)).
		Select(`COALESCE(SUM(subtotal_paise), 0) AS gross_revenue_paise,
		        COALESCE(SUM(discount_paise), 0) AS total_discount_paise,
		        COALESCE(SUM(total_paise), 0)    AS net_revenue_paise,
		        COUNT(*)                         AS total_orders`).
		Scan(&totals).Error
	if err != nil {
		return domain.Report{}, domain.WrapError(domain.CodeInternal, "report totals", err)
	}
	rep.GrossRevenuePaise = totals.GrossRevenuePaise
	rep.TotalDiscountPaise = totals.TotalDiscountPaise
	rep.NetRevenuePaise = totals.NetRevenuePaise
	rep.TotalOrders = totals.TotalOrders

	var coupons struct {
		Generated int64
		Available int64
		Redeemed  int64
	}
	err = db.Model(&Coupon{}).
		Select(`COUNT(*) AS generated,
		        COUNT(*) FILTER (WHERE status = 'available') AS available,
		        COUNT(*) FILTER (WHERE status = 'redeemed')  AS redeemed`).
		Scan(&coupons).Error
	if err != nil {
		return domain.Report{}, domain.WrapError(domain.CodeInternal, "report coupons", err)
	}
	rep.CouponsGenerated = coupons.Generated
	rep.CouponsAvailable = coupons.Available
	rep.CouponsRedeemed = coupons.Redeemed

	return rep, nil
}
