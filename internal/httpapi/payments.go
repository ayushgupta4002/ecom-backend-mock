package httpapi

import (
	"net/http"

	"github.com/ayush/checkout-rewards/internal/domain"
)

type webhookRequest struct {
	ProviderRef string `json:"provider_ref"`
	Status      string `json:"status"` // "success" or "failed"
}

// paymentWebhook is the endpoint a payment provider calls to settle a
// payment. It is safe to call repeatedly with the same provider_ref: the
// first delivery settles, later ones return the current state unchanged.
func (a *API) paymentWebhook(w http.ResponseWriter, r *http.Request) {
	var req webhookRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if req.ProviderRef == "" {
		writeError(w, domain.NewError(domain.CodeValidation, "provider_ref is required"))
		return
	}
	if req.Status != "success" && req.Status != "failed" {
		writeError(w, domain.NewError(domain.CodeValidation, `status must be "success" or "failed"`))
		return
	}

	order, err := a.repo.SettlePayment(r.Context(), req.ProviderRef, req.Status == "success")
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, order)
}

// cancelOrder releases a pending order's stock and coupon, or refunds a paid
// one (stock only -- see DECISIONS.md).
func (a *API) cancelOrder(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, err)
		return
	}
	order, err := a.repo.CancelOrder(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, order)
}
