package httpapi

import (
	"net/http"

	"github.com/ayush/checkout-rewards/internal/domain"
	"github.com/ayush/checkout-rewards/internal/repository"
)

type createCouponRequest struct {
	// UserID omitted (or null) creates a GLOBAL coupon that any customer
	// may redeem. Set it to grant a coupon to one specific customer.
	UserID *int64 `json:"user_id"`
	// DiscountPercent defaults to the configured x when omitted.
	DiscountPercent *int `json:"discount_percent"`
	// Code is generated when omitted.
	Code string `json:"code"`
}

// createCoupon is an administrative escape hatch. Milestone rewards mint
// themselves when a payment qualifies; this exists for what the automatic
// path deliberately cannot do -- goodwill grants, backfilling milestones a
// customer passed before automatic minting existed, and global promotional
// codes.
//
// No authentication is implemented (out of scope); in a real deployment
// this would require an admin-scoped credential, since it creates money off.
func (a *API) createCoupon(w http.ResponseWriter, r *http.Request) {
	var req createCouponRequest
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, err)
			return
		}
	}
	if req.UserID != nil && *req.UserID <= 0 {
		writeError(w, domain.NewError(domain.CodeValidation,
			"user_id must be a positive integer, or omitted for a global coupon"))
		return
	}

	coupon, err := a.repo.CreateAdminCoupon(r.Context(), repository.AdminCouponRequest{
		UserID:          req.UserID,
		DiscountPercent: req.DiscountPercent,
		Code:            req.Code,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, coupon)
}

func (a *API) getCoupon(w http.ResponseWriter, r *http.Request) {
	coupon, err := a.repo.GetCouponByCode(r.Context(), r.PathValue("code"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, coupon)
}

// getReport is an administrative, read-only operation. It never mutates
// state, so it is safe to call repeatedly.
func (a *API) getReport(w http.ResponseWriter, r *http.Request) {
	report, err := a.repo.GetReport(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}
