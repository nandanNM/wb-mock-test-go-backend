// Package handler holds the HTTP handlers and route registration. Handlers
// return errors and let httpx render them, so they read as plain happy-path
// logic.
package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"backend/internal/auth"
	"backend/internal/httpx"
	"backend/internal/logger"
	"backend/internal/middleware"
	"backend/internal/rbac"
	"backend/internal/repository"
)

// API groups handler dependencies (repositories, services, clients) so they are
// injected once at startup instead of using globals.
type API struct {
	startedAt time.Time
	version   string
	users     *repository.UserRepository
	// readiness checks the health of external dependencies (e.g. the DB).
	readiness func(ctx context.Context) error

	// Authentication wiring.
	auth            *auth.Service
	transport       *auth.Transport
	authn           *middleware.Authenticator
	csrf            *middleware.CSRF
	rbac            *rbac.Service
	successRedirect string
}

// Deps bundles everything New needs. Grouping into a struct keeps the
// constructor stable as dependencies grow.
type Deps struct {
	Version         string
	Users           *repository.UserRepository
	Readiness       func(ctx context.Context) error
	Auth            *auth.Service
	Transport       *auth.Transport
	Authenticator   *middleware.Authenticator
	CSRF            *middleware.CSRF
	RBAC            *rbac.Service
	SuccessRedirect string
}

// New constructs the API with its dependencies.
func New(d Deps) *API {
	return &API{
		startedAt:       time.Now(),
		version:         d.Version,
		users:           d.Users,
		readiness:       d.Readiness,
		auth:            d.Auth,
		transport:       d.Transport,
		authn:           d.Authenticator,
		csrf:            d.CSRF,
		rbac:            d.RBAC,
		successRedirect: d.SuccessRedirect,
	}
}

// Routes returns the application's router using Go 1.22+ method+path patterns,
// so no third-party router is needed. Per-route auth/RBAC/CSRF is applied by
// wrapping individual handlers.
func (a *API) Routes() *http.ServeMux {
	mux := http.NewServeMux()

	// protected requires a valid access token.
	protected := func(h httpx.HandlerFunc) http.Handler {
		return a.authn.Require(httpx.Handler(h))
	}
	// admin requires authentication AND the given permission.
	admin := func(perm string, h httpx.HandlerFunc) http.Handler {
		return a.authn.Require(a.rbac.RequirePermission(perm)(httpx.Handler(h)))
	}

	// Health (public).
	mux.Handle("GET /healthz", httpx.Handler(a.health))
	mux.Handle("GET /readyz", httpx.Handler(a.ready))

	// OAuth (public — they bootstrap authentication).
	mux.Handle("GET /v1/auth/google/start", httpx.Handler(a.googleStart))
	mux.Handle("GET /v1/auth/google/callback", httpx.Handler(a.googleCallback))

	// Refresh is cookie- or body-authenticated; CSRF-guarded for cookie callers.
	mux.Handle("POST /v1/auth/refresh", a.csrf.Protect(httpx.Handler(a.refresh)))

	// Authenticated session management.
	mux.Handle("POST /v1/auth/logout", protected(a.logout))
	mux.Handle("POST /v1/auth/logout-all", protected(a.logoutAll))
	mux.Handle("GET /v1/auth/sessions", protected(a.listSessions))
	mux.Handle("DELETE /v1/auth/sessions/{id}", protected(a.revokeSession))
	mux.Handle("GET /v1/me", protected(a.me))

	// User directory (requires the users:read permission).
	mux.Handle("GET /v1/users", admin("users:read", a.listUsers))
	mux.Handle("POST /v1/users", admin("users:read", a.createUser))
	mux.Handle("GET /v1/users/{id}", admin("users:read", a.getUser))

	// Admin.
	mux.Handle("POST /v1/admin/users/{id}/ban", admin("users:ban", a.adminSetStatus("banned")))
	mux.Handle("POST /v1/admin/users/{id}/suspend", admin("users:ban", a.adminSetStatus("suspended")))
	mux.Handle("POST /v1/admin/users/{id}/reinstate", admin("users:ban", a.adminSetStatus("active")))
	mux.Handle("POST /v1/admin/users/{id}/roles", admin("roles:manage", a.adminGrantRole))
	mux.Handle("DELETE /v1/admin/users/{id}/roles/{role}", admin("roles:manage", a.adminRevokeRole))
	mux.Handle("GET /v1/admin/audit", admin("audit:read", a.adminListAudit))

	return mux
}

// health is a liveness probe: is the process up?
func (a *API) health(w http.ResponseWriter, r *http.Request) error {
	httpx.JSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"version": a.version,
		"uptime":  time.Since(a.startedAt).String(),
	})
	return nil
}

// ready is a readiness probe: can the process serve traffic? It checks the DB.
func (a *API) ready(w http.ResponseWriter, r *http.Request) error {
	if a.readiness != nil {
		if err := a.readiness(r.Context()); err != nil {
			return httpx.Wrap(
				httpx.NewAPIError(http.StatusServiceUnavailable, "not_ready", "Service dependencies are unavailable."),
				err,
			)
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "ready"})
	return nil
}

// listUsers returns the most recent users, with an optional ?limit= param.
func (a *API) listUsers(w http.ResponseWriter, r *http.Request) error {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}

	users, err := a.users.List(r.Context(), limit)
	if err != nil {
		return httpx.Wrap(httpx.ErrInternal, err)
	}
	httpx.JSON(w, http.StatusOK, users)
	return nil
}

// getUser fetches a single user by id, returning a structured 404 if missing.
func (a *API) getUser(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")

	user, err := a.users.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return httpx.ErrNotFound.WithDetails(map[string]any{"id": id})
		}
		return httpx.Wrap(httpx.ErrInternal, err)
	}
	httpx.JSON(w, http.StatusOK, user)
	return nil
}

// createUserRequest is the expected POST body.
type createUserRequest struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// createUser validates input and persists a new user.
func (a *API) createUser(w http.ResponseWriter, r *http.Request) error {
	var req createUserRequest
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}

	// Input validation -> structured 422 with per-field detail.
	fields := map[string]any{}
	req.Name = strings.TrimSpace(req.Name)
	req.Email = strings.TrimSpace(req.Email)
	if req.Name == "" {
		fields["name"] = "is required"
	}
	if req.Email == "" {
		fields["email"] = "is required"
	} else if !strings.Contains(req.Email, "@") {
		fields["email"] = "must be a valid email address"
	}
	if len(fields) > 0 {
		return httpx.ErrValidation.WithDetails(fields)
	}

	user, err := a.users.Create(r.Context(), req.Name, req.Email)
	if err != nil {
		if errors.Is(err, repository.ErrDuplicate) {
			return httpx.ErrConflict.WithDetails(map[string]any{"email": "already in use"})
		}
		return httpx.Wrap(httpx.ErrInternal, err)
	}

	logger.FromContext(r.Context()).Info("user created", "user_id", user.ID)
	httpx.JSON(w, http.StatusCreated, user)
	return nil
}
