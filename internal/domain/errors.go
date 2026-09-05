package domain

import "fmt"

// Code is a stable, machine-readable error identifier returned to API
// clients alongside a human-readable message. Clients should branch on
// Code, never on Message text.
type Code string

const (
	CodeValidation            Code = "VALIDATION_ERROR"
	CodeNotFound              Code = "NOT_FOUND"
	CodeCartAlreadyCheckedOut Code = "CART_ALREADY_CHECKED_OUT"
	CodeInsufficientStock     Code = "INSUFFICIENT_STOCK"
	CodeInvalidCoupon         Code = "INVALID_COUPON"
	CodeCouponAlreadyRedeemed Code = "COUPON_ALREADY_REDEEMED"
	CodeCouponNotOwned        Code = "COUPON_NOT_OWNED"
	CodeOrderNotCancellable   Code = "ORDER_NOT_CANCELLABLE"
	CodeConflict              Code = "CONFLICT"
	CodeInternal              Code = "INTERNAL_ERROR"
)

// Error is the application-level error type. The HTTP layer maps Code to a
// status code; see httpapi/errors.go.
type Error struct {
	Code    Code
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.Err }

func NewError(code Code, message string) *Error {
	return &Error{Code: code, Message: message}
}

func WrapError(code Code, message string, err error) *Error {
	return &Error{Code: code, Message: message, Err: err}
}
