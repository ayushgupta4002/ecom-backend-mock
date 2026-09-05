package httpapi

import (
	"net/http"

	"github.com/ayush/checkout-rewards/internal/domain"
)

type createCartRequest struct {
	UserID int64 `json:"user_id"`
}

// createCart opens a cart for a user. Authentication is out of scope, so
// the client supplies user_id; in production this would come from the
// authenticated session.
func (a *API) createCart(w http.ResponseWriter, r *http.Request) {
	var req createCartRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}

	cart, err := a.repo.CreateCart(r.Context(), req.UserID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, cart)
}

func (a *API) getCart(w http.ResponseWriter, r *http.Request) {
	a.writeCart(w, r, http.StatusOK)
}

// writeCart responds with the cart's current state. Cart mutations all
// return the updated cart, so the client never has to re-fetch it.
func (a *API) writeCart(w http.ResponseWriter, r *http.Request, status int) {
	cartID, err := pathID(r, "id")
	if err != nil {
		writeError(w, err)
		return
	}
	view, err := a.repo.GetCartView(r.Context(), cartID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, status, view)
}

type addItemRequest struct {
	ProductID int64 `json:"product_id"`
	Quantity  int   `json:"quantity"`
}

func (a *API) addItem(w http.ResponseWriter, r *http.Request) {
	var req addItemRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	cartID, err := pathID(r, "id")
	if err != nil {
		writeError(w, err)
		return
	}
	if req.ProductID <= 0 {
		writeError(w, domain.NewError(domain.CodeValidation, "a valid product_id is required"))
		return
	}
	if err := a.repo.AddItem(r.Context(), cartID, req.ProductID, req.Quantity); err != nil {
		writeError(w, err)
		return
	}
	a.writeCart(w, r, http.StatusCreated)
}

type updateItemRequest struct {
	Quantity int `json:"quantity"`
}

func (a *API) updateItem(w http.ResponseWriter, r *http.Request) {
	var req updateItemRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	cartID, err := pathID(r, "id")
	if err != nil {
		writeError(w, err)
		return
	}
	productID, err := pathID(r, "productId")
	if err != nil {
		writeError(w, err)
		return
	}
	if err := a.repo.UpdateItemQuantity(r.Context(), cartID, productID, req.Quantity); err != nil {
		writeError(w, err)
		return
	}
	a.writeCart(w, r, http.StatusOK)
}

func (a *API) removeItem(w http.ResponseWriter, r *http.Request) {
	cartID, err := pathID(r, "id")
	if err != nil {
		writeError(w, err)
		return
	}
	productID, err := pathID(r, "productId")
	if err != nil {
		writeError(w, err)
		return
	}
	if err := a.repo.RemoveItem(r.Context(), cartID, productID); err != nil {
		writeError(w, err)
		return
	}
	a.writeCart(w, r, http.StatusOK)
}

type checkoutRequest struct {
	CouponCode *string `json:"coupon_code,omitempty"`
}

func (a *API) checkout(w http.ResponseWriter, r *http.Request) {
	var req checkoutRequest
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, err)
			return
		}
	}
	cartID, err := pathID(r, "id")
	if err != nil {
		writeError(w, err)
		return
	}
	order, err := a.repo.Checkout(r.Context(), cartID, req.CouponCode)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, order)
}
