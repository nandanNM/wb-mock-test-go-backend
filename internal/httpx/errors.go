// Package httpx contains HTTP transport helpers: structured error types and
// consistent JSON response writers shared by every handler.
package httpx

import (
	"errors"
	"fmt"
	"net/http"
)

// APIError is a structured, client-safe error. Handlers return these so the
// transport layer can produce a consistent error response and pick the right
// HTTP status code. The internal Err (if any) is logged but never sent to the
// client.
type APIError struct {
	Status  int            // HTTP status code
	Code    string         // stable, machine-readable error code (e.g. "not_found")
	Message string         // human-readable, client-safe message
	Details map[string]any // optional structured detail (e.g. field validation)
	Err     error          // wrapped internal error, for logging only
}

// Error implements the error interface.
func (e *APIError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

// Unwrap supports errors.Is / errors.As against the wrapped internal error.
func (e *APIError) Unwrap() error { return e.Err }

// withErr returns a shallow copy wrapping the given internal error.
func (e *APIError) withErr(err error) *APIError {
	clone := *e
	clone.Err = err
	return &clone
}

// Wrap attaches an internal error to a sentinel APIError so the original cause
// is logged while the client still receives the safe message.
//
//	return httpx.Wrap(httpx.ErrInternal, err)
func Wrap(base *APIError, err error) *APIError {
	return base.withErr(err)
}

// WithDetails returns a copy of the error carrying structured details.
func (e *APIError) WithDetails(details map[string]any) *APIError {
	clone := *e
	clone.Details = details
	return &clone
}

// Sentinel errors covering the common cases. Construct ad-hoc errors with
// NewAPIError when you need a custom message.
var (
	ErrBadRequest   = &APIError{Status: http.StatusBadRequest, Code: "bad_request", Message: "The request was invalid."}
	ErrUnauthorized = &APIError{Status: http.StatusUnauthorized, Code: "unauthorized", Message: "Authentication is required."}
	ErrForbidden    = &APIError{Status: http.StatusForbidden, Code: "forbidden", Message: "You do not have access to this resource."}
	ErrNotFound     = &APIError{Status: http.StatusNotFound, Code: "not_found", Message: "The requested resource was not found."}
	ErrConflict     = &APIError{Status: http.StatusConflict, Code: "conflict", Message: "The request conflicts with the current state."}
	ErrValidation   = &APIError{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: "Validation failed."}
	ErrInternal     = &APIError{Status: http.StatusInternalServerError, Code: "internal_error", Message: "An unexpected error occurred."}
)

// NewAPIError builds a custom client-safe error.
func NewAPIError(status int, code, message string) *APIError {
	return &APIError{Status: status, Code: code, Message: message}
}

// asAPIError normalizes any error into an *APIError. Unknown errors become a
// generic 500 so we never leak internal details to the client.
func asAPIError(err error) *APIError {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	return ErrInternal.withErr(err)
}
