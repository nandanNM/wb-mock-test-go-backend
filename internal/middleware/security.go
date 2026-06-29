package middleware

import (
	"context"
	"net/http"
	"time"
)

// SecurityHeaders sets a baseline of safe HTTP response headers. Tune these for
// your app (e.g. a stricter Content-Security-Policy for browser frontends).
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// Timeout enforces a per-request deadline by attaching a context timeout. When
// it fires, the handler's ctx is cancelled; well-behaved handlers (and the DB
// driver) abort their work. The response writer is left to the handler, so this
// pairs with handlers that respect ctx.Err().
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
