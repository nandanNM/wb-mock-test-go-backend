package httpx

import (
	"encoding/json"
	"net/http"

	"backend/internal/logger"
	"backend/internal/middleware"
)

// envelope is the consistent shape of every JSON response. Exactly one of
// Data or Error is populated.
type envelope struct {
	Data  any           `json:"data,omitempty"`
	Error *errorPayload `json:"error,omitempty"`
}

type errorPayload struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
	RequestID string         `json:"request_id,omitempty"`
}

// HandlerFunc is like http.HandlerFunc but returns an error, letting handlers
// stay focused on the happy path and delegate error rendering to the transport.
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

// Handler adapts an error-returning HandlerFunc into a standard http.Handler,
// rendering any returned error as a consistent JSON envelope.
func Handler(fn HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			Error(w, r, err)
		}
	})
}

// JSON writes a success response with the given status code.
func JSON(w http.ResponseWriter, status int, data any) {
	write(w, status, envelope{Data: data})
}

// Error writes a structured error response, logging the internal cause. The
// status code and client-safe message come from the (normalized) APIError.
func Error(w http.ResponseWriter, r *http.Request, err error) {
	apiErr := asAPIError(err)
	reqID := middleware.RequestIDFromContext(r.Context())

	// Log server-side errors with full internal detail; client errors at debug.
	log := logger.FromContext(r.Context())
	if apiErr.Status >= http.StatusInternalServerError {
		log.Error("request failed",
			"error", apiErr.Error(),
			"code", apiErr.Code,
			"status", apiErr.Status,
		)
	} else {
		log.Debug("request rejected",
			"error", apiErr.Error(),
			"code", apiErr.Code,
			"status", apiErr.Status,
		)
	}

	write(w, apiErr.Status, envelope{
		Error: &errorPayload{
			Code:      apiErr.Code,
			Message:   apiErr.Message,
			Details:   apiErr.Details,
			RequestID: reqID,
		},
	})
}

func write(w http.ResponseWriter, status int, body envelope) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	// If encoding fails the status line is already sent; nothing more we can do
	// but the error is rare (only on broken connections).
	_ = json.NewEncoder(w).Encode(body)
}

// Decode reads and strictly decodes a JSON request body into dst, returning a
// client-safe bad-request error on malformed input.
func Decode(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return Wrap(ErrBadRequest.WithDetails(map[string]any{"reason": "invalid JSON body"}), err)
	}
	return nil
}
