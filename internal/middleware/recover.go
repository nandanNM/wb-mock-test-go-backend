package middleware

import (
	"encoding/json"
	"net/http"
	"runtime/debug"

	"backend/internal/logger"
)

// Recover converts an unhandled panic in a handler into a logged 500 response,
// keeping the server alive. The stack trace is logged (never sent to clients).
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log := logger.FromContext(r.Context())
				log.Error("panic recovered",
					"panic", rec,
					"stack", string(debug.Stack()),
				)

				// Avoid writing a body if the handler already started the response.
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]any{
						"code":       "internal_error",
						"message":    "An unexpected error occurred.",
						"request_id": RequestIDFromContext(r.Context()),
					},
				})
			}
		}()

		next.ServeHTTP(w, r)
	})
}

// Chain composes middleware so the first listed runs outermost (first to see
// the request, last to see the response):
//
//	Chain(h, RequestID, Recover, Logger(log))
func Chain(h http.Handler, middlewares ...func(http.Handler) http.Handler) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		h = middlewares[i](h)
	}
	return h
}
