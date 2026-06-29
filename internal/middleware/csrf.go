package middleware

import (
	"net/http"

	"backend/internal/auth"
)

// CSRF guards cookie-authenticated, state-changing endpoints with an HMAC
// double-submit check. It engages ONLY when the request carries the refresh
// cookie (i.e. a browser/cookie-authenticated call); bearer-token (native, and
// web API) requests are immune to CSRF by construction and pass through.
type CSRF struct {
	transport *auth.Transport
}

// NewCSRF builds CSRF middleware bound to the auth Transport (for token validation).
func NewCSRF(transport *auth.Transport) *CSRF {
	return &CSRF{transport: transport}
}

// Protect is the middleware.
func (c *CSRF) Protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only cookie-authenticated requests are CSRF-relevant.
		cookie, err := r.Cookie(auth.RefreshCookieName)
		if err != nil || cookie.Value == "" {
			next.ServeHTTP(w, r)
			return
		}

		csrfCookie, err := r.Cookie(auth.CSRFCookieName)
		if err != nil || !c.transport.ValidateCSRF(csrfCookie.Value, r.Header.Get(auth.CSRFHeader)) {
			WriteError(w, r, http.StatusForbidden, "csrf_failed", "Missing or invalid CSRF token.")
			return
		}
		next.ServeHTTP(w, r)
	})
}
