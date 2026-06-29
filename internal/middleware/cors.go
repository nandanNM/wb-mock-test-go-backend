package middleware

import (
	"net/http"
	"strings"
)

// CORS returns middleware that applies Cross-Origin Resource Sharing headers.
// Pass the list of allowed origins (use {"*"} to allow any). Preflight OPTIONS
// requests are answered directly with 204.
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	allowAll := len(allowedOrigins) == 1 && allowedOrigins[0] == "*"
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[o] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			if origin != "" && (allowAll || allowed[origin]) {
				if allowAll {
					w.Header().Set("Access-Control-Allow-Origin", "*")
				} else {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					// Vary so caches don't serve the wrong origin's response.
					w.Header().Add("Vary", "Origin")
					// Credentials (cookies) require an explicit origin, never "*".
					w.Header().Set("Access-Control-Allow-Credentials", "true")
				}
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-CSRF-Token, "+HeaderRequestID)
				w.Header().Set("Access-Control-Expose-Headers", HeaderRequestID)
				w.Header().Set("Access-Control-Max-Age", "300")
			}

			// Short-circuit preflight requests.
			if r.Method == http.MethodOptions && strings.TrimSpace(r.Header.Get("Access-Control-Request-Method")) != "" {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
