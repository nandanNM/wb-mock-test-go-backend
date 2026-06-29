// Package middleware contains cross-cutting HTTP middleware: request IDs,
// structured request/response logging, and panic recovery. Middleware is
// composed in order by the Chain helper.
package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

// HeaderRequestID is the canonical header used to propagate a request ID across
// service boundaries (incoming and outgoing).
const HeaderRequestID = "X-Request-ID"

type ctxKey struct{}

var requestIDKey = ctxKey{}

// RequestID assigns a unique ID to every request. It reuses an inbound
// X-Request-ID when present (so a trace can span multiple services) and
// otherwise generates one. The ID is stored in the context and echoed back in
// the response header.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(HeaderRequestID)
		if id == "" {
			id = newID()
		}

		ctx := context.WithValue(r.Context(), requestIDKey, id)
		w.Header().Set(HeaderRequestID, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestIDFromContext returns the request ID, or "" if none was set.
func RequestIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey).(string); ok {
		return id
	}
	return ""
}

// newID returns a random 128-bit hex identifier. crypto/rand never fails on
// supported platforms; if it ever did we'd rather have an empty ID than panic.
func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}
