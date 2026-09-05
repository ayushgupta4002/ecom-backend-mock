package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/ayush/checkout-rewards/internal/domain"
)

type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

var codeToStatus = map[domain.Code]int{
	domain.CodeValidation:            http.StatusBadRequest,
	domain.CodeNotFound:              http.StatusNotFound,
	domain.CodeCartAlreadyCheckedOut: http.StatusConflict,
	domain.CodeInsufficientStock:     http.StatusConflict,
	domain.CodeInvalidCoupon:         http.StatusBadRequest,
	domain.CodeCouponAlreadyRedeemed: http.StatusConflict,
	domain.CodeCouponNotOwned:        http.StatusForbidden,
	domain.CodeOrderNotCancellable:   http.StatusConflict,
	domain.CodeConflict:              http.StatusConflict,
	domain.CodeInternal:              http.StatusInternalServerError,
}

func writeError(w http.ResponseWriter, err error) {
	var appErr *domain.Error
	if errors.As(err, &appErr) {
		status, ok := codeToStatus[appErr.Code]
		if !ok {
			status = http.StatusInternalServerError
		}
		if status == http.StatusInternalServerError {
			log.Printf("internal error: %v", appErr)
		}
		writeJSON(w, status, errorResponse{Code: string(appErr.Code), Message: appErr.Message})
		return
	}
	log.Printf("unhandled error: %v", err)
	writeJSON(w, http.StatusInternalServerError, errorResponse{Code: string(domain.CodeInternal), Message: "internal server error"})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Printf("encode response: %v", err)
	}
}

// pathID reads a numeric path parameter. IDs are sequential integers, so a
// non-numeric value is a client mistake, not a missing resource.
func pathID(r *http.Request, name string) (int64, error) {
	raw := r.PathValue(name)
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, domain.NewError(domain.CodeValidation,
			fmt.Sprintf("%s must be a positive integer, got %q", name, raw))
	}
	return id, nil
}

func decodeJSON(r *http.Request, dst any) error {
	if r.Body == nil {
		return domain.NewError(domain.CodeValidation, "request body is required")
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return domain.WrapError(domain.CodeValidation, "invalid request body", err)
	}
	return nil
}
