package handler

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"

	"backend/internal/auth"
	"backend/internal/httpx"
	"backend/internal/middleware"
	"backend/internal/repository"
)

// --- OAuth ---------------------------------------------------------------

// googleStart begins the Google OAuth flow. Web clients are redirected to the
// consent screen; native clients receive the URL as JSON to open themselves.
func (a *API) googleStart(w http.ResponseWriter, r *http.Request) error {
	ct := clientTypeFromQuery(r)
	redirectURI := r.URL.Query().Get("redirect_uri")
	if redirectURI == "" {
		redirectURI = a.successRedirect
	}

	url, err := a.auth.StartGoogleLogin(r.Context(), ct, redirectURI)
	if err != nil {
		if errors.Is(err, auth.ErrGoogleNotConfigured) {
			return httpx.NewAPIError(http.StatusNotImplemented, "google_not_configured", "Google login is not configured.")
		}
		return httpx.Wrap(httpx.ErrInternal, err)
	}

	if ct == auth.ClientWeb {
		http.Redirect(w, r, url, http.StatusFound)
		return nil
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"authorization_url": url})
	return nil
}

// googleCallback handles Google's redirect back: exchange code, provision, issue
// a session, then deliver tokens (cookies+redirect for web, JSON for native).
func (a *API) googleCallback(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		return httpx.NewAPIError(http.StatusBadRequest, "oauth_denied", "Authorization was denied: "+e)
	}
	state, code := q.Get("state"), q.Get("code")
	if state == "" || code == "" {
		return httpx.ErrBadRequest.WithDetails(map[string]any{"reason": "missing state or code"})
	}

	result, ct, redirect, err := a.auth.HandleGoogleCallback(r.Context(), state, code, deviceFromRequest(r), metaFromRequest(r))
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrInvalidState):
			return httpx.NewAPIError(http.StatusBadRequest, "invalid_state", "Login session expired or invalid; start again.")
		case errors.Is(err, auth.ErrAccountNotActive):
			return httpx.NewAPIError(http.StatusForbidden, "account_inactive", "This account is not active.")
		case errors.Is(err, auth.ErrGoogleNotConfigured):
			return httpx.NewAPIError(http.StatusNotImplemented, "google_not_configured", "Google login is not configured.")
		default:
			return httpx.Wrap(httpx.NewAPIError(http.StatusBadGateway, "oauth_failed", "Could not complete Google login."), err)
		}
	}

	resp := a.transport.Deliver(w, ct, result.Tokens)

	// Web clients with a success URL: cookies are set, redirect the browser.
	// The SPA then calls /v1/auth/refresh (cookie) to obtain an access token.
	if ct == auth.ClientWeb && redirect != "" {
		http.Redirect(w, r, redirect, http.StatusFound)
		return nil
	}
	httpx.JSON(w, http.StatusOK, resp)
	return nil
}

// --- Tokens / sessions ---------------------------------------------------

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// refresh rotates the refresh token and issues a new access token.
func (a *API) refresh(w http.ResponseWriter, r *http.Request) error {
	ct := auth.DetectClientType(r)

	var raw string
	if ct == auth.ClientWeb {
		raw = a.transport.RefreshTokenFromCookie(r)
	} else {
		var req refreshRequest
		if err := httpx.Decode(r, &req); err != nil {
			return err
		}
		raw = strings.TrimSpace(req.RefreshToken)
	}
	if raw == "" {
		return httpx.NewAPIError(http.StatusUnauthorized, "no_refresh_token", "No refresh token supplied.")
	}

	tokens, err := a.auth.Refresh(r.Context(), raw, deviceFromRequest(r), metaFromRequest(r))
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrRefreshInProgress):
			return httpx.NewAPIError(http.StatusConflict, "refresh_in_progress", "A concurrent refresh is in progress; retry.")
		case errors.Is(err, auth.ErrRefreshReuse):
			a.transport.ClearAuthCookies(w)
			return httpx.NewAPIError(http.StatusUnauthorized, "token_reuse", "Refresh token reuse detected; session revoked. Sign in again.")
		case errors.Is(err, auth.ErrAccountNotActive):
			a.transport.ClearAuthCookies(w)
			return httpx.NewAPIError(http.StatusForbidden, "account_inactive", "This account is not active.")
		case errors.Is(err, auth.ErrRefreshInvalid):
			a.transport.ClearAuthCookies(w)
			return httpx.NewAPIError(http.StatusUnauthorized, "invalid_refresh_token", "Refresh token invalid or expired.")
		default:
			return httpx.Wrap(httpx.ErrInternal, err)
		}
	}

	httpx.JSON(w, http.StatusOK, a.transport.Deliver(w, ct, tokens))
	return nil
}

// logout revokes the current session.
func (a *API) logout(w http.ResponseWriter, r *http.Request) error {
	p, _ := middleware.PrincipalFromContext(r.Context())
	if err := a.auth.Logout(r.Context(), p.SessionID, p.UserID, metaFromRequest(r)); err != nil && !errors.Is(err, repository.ErrNotFound) {
		return httpx.Wrap(httpx.ErrInternal, err)
	}
	a.transport.ClearAuthCookies(w)
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "logged_out"})
	return nil
}

// logoutAll revokes every session for the current user.
func (a *API) logoutAll(w http.ResponseWriter, r *http.Request) error {
	p, _ := middleware.PrincipalFromContext(r.Context())
	n, err := a.auth.LogoutAll(r.Context(), p.UserID, metaFromRequest(r))
	if err != nil {
		return httpx.Wrap(httpx.ErrInternal, err)
	}
	a.transport.ClearAuthCookies(w)
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "logged_out_all", "revoked": n})
	return nil
}

// me returns the current user with roles and permissions.
func (a *API) me(w http.ResponseWriter, r *http.Request) error {
	p, _ := middleware.PrincipalFromContext(r.Context())
	user, err := a.auth.UserByID(r.Context(), p.UserID)
	if err != nil {
		return httpx.Wrap(httpx.ErrInternal, err)
	}
	perms, err := a.auth.PermissionsForRoles(r.Context(), p.Roles)
	if err != nil {
		return httpx.Wrap(httpx.ErrInternal, err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"user":        user,
		"roles":       p.Roles,
		"permissions": perms,
		"session_id":  p.SessionID,
	})
	return nil
}

// listSessions lists the current user's active devices.
func (a *API) listSessions(w http.ResponseWriter, r *http.Request) error {
	p, _ := middleware.PrincipalFromContext(r.Context())
	sessions, err := a.auth.ListSessions(r.Context(), p.UserID, p.SessionID)
	if err != nil {
		return httpx.Wrap(httpx.ErrInternal, err)
	}
	httpx.JSON(w, http.StatusOK, sessions)
	return nil
}

// revokeSession signs a specific device out.
func (a *API) revokeSession(w http.ResponseWriter, r *http.Request) error {
	p, _ := middleware.PrincipalFromContext(r.Context())
	id := r.PathValue("id")
	err := a.auth.RevokeSession(r.Context(), id, p.UserID, metaFromRequest(r))
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return httpx.ErrNotFound.WithDetails(map[string]any{"id": id})
		}
		return httpx.Wrap(httpx.ErrInternal, err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "revoked"})
	return nil
}

// --- Admin ---------------------------------------------------------------

type statusRequest struct {
	Reason string `json:"reason"`
}

// adminSetStatus handles ban/suspend/reinstate based on the target status.
func (a *API) adminSetStatus(status string) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		actor, _ := middleware.PrincipalFromContext(r.Context())
		targetID := r.PathValue("id")

		var req statusRequest
		if r.ContentLength > 0 {
			if err := httpx.Decode(r, &req); err != nil {
				return err
			}
		}

		err := a.auth.SetUserStatus(r.Context(), actor.UserID, targetID, status, req.Reason, metaFromRequest(r))
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return httpx.ErrNotFound.WithDetails(map[string]any{"id": targetID})
			}
			return httpx.Wrap(httpx.ErrInternal, err)
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"status": status, "user_id": targetID})
		return nil
	}
}

type roleRequest struct {
	Role string `json:"role"`
}

// adminGrantRole grants a role to a user.
func (a *API) adminGrantRole(w http.ResponseWriter, r *http.Request) error {
	actor, _ := middleware.PrincipalFromContext(r.Context())
	targetID := r.PathValue("id")

	var req roleRequest
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	if req.Role == "" {
		return httpx.ErrValidation.WithDetails(map[string]any{"role": "is required"})
	}

	err := a.auth.GrantRole(r.Context(), actor.UserID, targetID, req.Role, metaFromRequest(r))
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return httpx.ErrNotFound.WithDetails(map[string]any{"role": req.Role})
		}
		return httpx.Wrap(httpx.ErrInternal, err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "granted", "user_id": targetID, "role": req.Role})
	return nil
}

// adminRevokeRole removes a role from a user.
func (a *API) adminRevokeRole(w http.ResponseWriter, r *http.Request) error {
	actor, _ := middleware.PrincipalFromContext(r.Context())
	targetID := r.PathValue("id")
	role := r.PathValue("role")

	if err := a.auth.RevokeRole(r.Context(), actor.UserID, targetID, role, metaFromRequest(r)); err != nil {
		return httpx.Wrap(httpx.ErrInternal, err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "revoked", "user_id": targetID, "role": role})
	return nil
}

// adminListAudit returns recent audit events (optionally ?user_id=, ?limit=).
func (a *API) adminListAudit(w http.ResponseWriter, r *http.Request) error {
	userID := r.URL.Query().Get("user_id")
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	records, err := a.auth.ListAudit(r.Context(), userID, limit)
	if err != nil {
		return httpx.Wrap(httpx.ErrInternal, err)
	}
	httpx.JSON(w, http.StatusOK, records)
	return nil
}

// --- helpers -------------------------------------------------------------

func clientTypeFromQuery(r *http.Request) auth.ClientType {
	if strings.EqualFold(r.URL.Query().Get("client"), "native") {
		return auth.ClientNative
	}
	return auth.DetectClientType(r)
}

func deviceFromRequest(r *http.Request) auth.DeviceInfo {
	return auth.DeviceInfo{
		UserAgent: r.UserAgent(),
		IP:        clientIP(r),
		Label:     r.UserAgent(),
	}
}

func metaFromRequest(r *http.Request) auth.ReqMeta {
	return auth.ReqMeta{
		IP:        clientIP(r),
		UserAgent: r.UserAgent(),
		RequestID: middleware.RequestIDFromContext(r.Context()),
	}
}

// clientIP extracts the originating client IP, honoring common proxy headers.
// It returns a bare IP (no port, no IPv6 brackets) suitable for an inet column.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
		return xrip
	}
	// SplitHostPort correctly handles IPv6 (e.g. "[::1]:54321" -> "::1").
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
