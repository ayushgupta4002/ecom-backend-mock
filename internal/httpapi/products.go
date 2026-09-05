package httpapi

import "net/http"

func (a *API) listProducts(w http.ResponseWriter, r *http.Request) {
	products, err := a.repo.ListProducts(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, products)
}

func (a *API) getProduct(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, err)
		return
	}
	product, err := a.repo.GetProduct(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, product)
}
