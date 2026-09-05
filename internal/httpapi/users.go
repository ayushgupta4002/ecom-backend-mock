package httpapi

import "net/http"

func (a *API) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := a.repo.ListUsers(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (a *API) getUser(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, err)
		return
	}
	user, err := a.repo.GetUser(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, user)
}

// getUserRewards shows a user's progress toward their next coupon plus the
// coupons they have already earned.
func (a *API) getUserRewards(w http.ResponseWriter, r *http.Request) {
	userID, err := pathID(r, "id")
	if err != nil {
		writeError(w, err)
		return
	}

	status, err := a.repo.GetRewardStatus(r.Context(), userID)
	if err != nil {
		writeError(w, err)
		return
	}
	coupons, err := a.repo.ListCouponsForUser(r.Context(), userID)
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  status,
		"coupons": coupons,
	})
}
