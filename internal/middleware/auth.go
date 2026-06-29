package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"backend/internal/auth"
)

// Principal is the authenticated caller, derived from a verified access token
// and injected into the request context for handlers and authorization checks.
type Principal struct {
	UserID    string
	SessionID string
	Roles     []string
}

type principalCtxKey struct{}

// PrincipalFromContext returns the authenticated principal, if any.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalCtxKey{}).(Principal)
	return p, ok
}

// Authenticator verifies access tokens and enforces account/session status.
type Authenticator struct {
	tokens *auth.TokenService
	cache  auth.StatusCache
}

// NewAuthenticator builds an Authenticator.
func NewAuthenticator(tokens *auth.TokenService, cache auth.StatusCache) *Authenticator {
	return &Authenticator{tokens: tokens, cache: cache}
}

// Require is middleware that rejects unauthenticated requests. On success it
// injects the Principal into the context.
func (a *Authenticator) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := bearerToken(r)
		if raw == "" {
			WriteError(w, r, http.StatusUnauthorized, "unauthorized", "Authentication is required.")
			return
		}

		claims, err := a.tokens.ParseAccessToken(raw)
		if err != nil {
			if errors.Is(err, auth.ErrAccessTokenExpired) {
				WriteError(w, r, http.StatusUnauthorized, "token_expired", "Access token expired; refresh it.")
				return
			}
			WriteError(w, r, http.StatusUnauthorized, "unauthorized", "Invalid access token.")
			return
		}

		// Freshness gate: bans/suspensions/logouts take effect within the cache TTL.
		status, err := a.cache.Get(r.Context(), claims.Subject, claims.SessionID)
		if err != nil {
			WriteError(w, r, http.StatusServiceUnavailable, "auth_unavailable", "Could not verify account status.")
			return
		}
		switch {
		case status.SessionRevoked:
			WriteError(w, r, http.StatusUnauthorized, "session_revoked", "This session is no longer valid.")
			return
		case status.UserStatus == "banned":
			WriteError(w, r, http.StatusForbidden, "account_banned", "This account has been banned.")
			return
		case status.UserStatus == "suspended":
			WriteError(w, r, http.StatusForbidden, "account_suspended", "This account is suspended.")
			return
		case !status.Active():
			WriteError(w, r, http.StatusForbidden, "account_inactive", "This account is not active.")
			return
		}

		p := Principal{UserID: claims.Subject, SessionID: claims.SessionID, Roles: claims.Roles}
		ctx := context.WithValue(r.Context(), principalCtxKey{}, p)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	const prefix = "Bearer "
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return ""
}

// WriteError writes the standard JSON error envelope directly. Used by
// middleware (which cannot import httpx without an import cycle) to stay
// consistent with httpx's response shape.
func WriteError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":       code,
			"message":    message,
			"request_id": RequestIDFromContext(r.Context()),
		},
	})
}
