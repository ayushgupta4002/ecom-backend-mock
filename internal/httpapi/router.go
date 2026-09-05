// Package httpapi wires HTTP routes to the repository. Authentication is
// out of scope for this exercise (per the assignment); routes under
// /admin/ are called out as administrative in README.md.
package httpapi

import (
	"net/http"

	"github.com/ayush/checkout-rewards/internal/repository"
)

type API struct {
	repo *repository.Repo
}

func New(repo *repository.Repo) http.Handler {
	a := &API{repo: repo}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", a.health)

	mux.HandleFunc("GET /users", a.listUsers)
	mux.HandleFunc("GET /users/{id}", a.getUser)
	mux.HandleFunc("GET /users/{id}/rewards", a.getUserRewards)

	mux.HandleFunc("GET /products", a.listProducts)
	mux.HandleFunc("GET /products/{id}", a.getProduct)

	mux.HandleFunc("POST /carts", a.createCart)
	mux.HandleFunc("GET /carts/{id}", a.getCart)
	mux.HandleFunc("POST /carts/{id}/items", a.addItem)
	mux.HandleFunc("PUT /carts/{id}/items/{productId}", a.updateItem)
	mux.HandleFunc("DELETE /carts/{id}/items/{productId}", a.removeItem)
	mux.HandleFunc("POST /carts/{id}/checkout", a.checkout)

	mux.HandleFunc("GET /orders/{id}", a.getOrder)
	mux.HandleFunc("POST /orders/{id}/cancel", a.cancelOrder)

	// Called by the payment provider. Idempotent on provider_ref.
	mux.HandleFunc("POST /payments/webhook", a.paymentWebhook)

	// Administrative operations. No auth is implemented (out of scope per
	// the assignment); in production these would sit behind an admin-only
	// authorization check.
	mux.HandleFunc("POST /admin/coupons", a.createCoupon)
	mux.HandleFunc("GET /admin/coupons/{code}", a.getCoupon)
	mux.HandleFunc("GET /admin/report", a.getReport)

	return logging(mux)
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
