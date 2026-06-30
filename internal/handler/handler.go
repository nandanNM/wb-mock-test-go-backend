// Package handler holds the HTTP handlers and route registration. Handlers
// return errors and let httpx render them, so they read as plain happy-path
// logic.
package handler

import (
	"context"
	"net/http"
	"time"

	"backend/internal/audit"
	"backend/internal/auth"
	"backend/internal/httpx"
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

	// Dashboard wiring.
	auditRec  *audit.Recorder
	subjects  *repository.SubjectRepository
	chapters  *repository.ChapterRepository
	notes     *repository.ChapterNoteRepository
	questions *repository.QuestionRepository
	tests     *repository.TestRepository
	sessions  *repository.SessionRepository
	roles     *repository.RoleRepository
	attempts  *repository.AttemptRepository
	battles   *repository.BattleRepository
	follows   *repository.FollowRepository
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

	Audit     *audit.Recorder
	Subjects  *repository.SubjectRepository
	Chapters  *repository.ChapterRepository
	Notes     *repository.ChapterNoteRepository
	Questions *repository.QuestionRepository
	Tests     *repository.TestRepository
	Sessions  *repository.SessionRepository
	Roles     *repository.RoleRepository
	Attempts  *repository.AttemptRepository
	Battles   *repository.BattleRepository
	Follows   *repository.FollowRepository
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
		auditRec:        d.Audit,
		subjects:        d.Subjects,
		chapters:        d.Chapters,
		notes:           d.Notes,
		questions:       d.Questions,
		tests:           d.Tests,
		sessions:        d.Sessions,
		roles:           d.Roles,
		attempts:        d.Attempts,
		battles:         d.Battles,
		follows:         d.Follows,
	}
}

// principalUserID returns the authenticated caller's user id.
func principalUserID(r *http.Request) (string, bool) {
	p, ok := middleware.PrincipalFromContext(r.Context())
	return p.UserID, ok
}

// auditDash records a dashboard mutation by the current admin (best-effort).
func (a *API) auditDash(r *http.Request, eventType string, detail map[string]any) {
	if a.auditRec == nil {
		return
	}
	p, _ := middleware.PrincipalFromContext(r.Context())
	uid := p.UserID
	a.auditRec.Record(r.Context(), repository.AuditEvent{
		EventType: eventType,
		UserID:    &uid,
		IP:        clientIP(r),
		UserAgent: r.UserAgent(),
		RequestID: middleware.RequestIDFromContext(r.Context()),
		Detail:    detail,
	})
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

	// Admin.
	mux.Handle("POST /v1/admin/users/{id}/ban", admin("users:ban", a.adminSetStatus("banned")))
	mux.Handle("POST /v1/admin/users/{id}/suspend", admin("users:ban", a.adminSetStatus("suspended")))
	mux.Handle("POST /v1/admin/users/{id}/reinstate", admin("users:ban", a.adminSetStatus("active")))
	mux.Handle("POST /v1/admin/users/{id}/roles", admin("roles:manage", a.adminGrantRole))
	mux.Handle("DELETE /v1/admin/users/{id}/roles/{role}", admin("roles:manage", a.adminRevokeRole))
	mux.Handle("GET /v1/admin/audit", admin("audit:read", a.adminListAudit))

	a.dashboardRoutes(mux, admin)
	return mux
}

// crud registers list/create/get/update/delete for a resource under the given
// permission. Pass nil handlers to skip an action.
type crudSet struct {
	list, create, get, update, delete httpx.HandlerFunc
}

// dashboardRoutes mounts the admin/super-admin dashboard CRUD + read APIs.
// `admin(perm, h)` enforces authentication + the named permission (super_admin
// bypasses all checks). Permissions the admin role lacks (users:delete,
// attempts:delete) therefore become super-admin-only automatically.
func (a *API) dashboardRoutes(mux *http.ServeMux, admin func(string, httpx.HandlerFunc) http.Handler) {
	// register mounts CRUD with separate read (list/get) and write
	// (create/update/delete) permissions, so a view-only admin tier is possible.
	register := func(resource, readPerm, writePerm string, c crudSet) {
		base := "/v1/admin/" + resource
		if c.list != nil {
			mux.Handle("GET "+base, admin(readPerm, c.list))
		}
		if c.get != nil {
			mux.Handle("GET "+base+"/{id}", admin(readPerm, c.get))
		}
		if c.create != nil {
			mux.Handle("POST "+base, admin(writePerm, c.create))
		}
		if c.update != nil {
			mux.Handle("PATCH "+base+"/{id}", admin(writePerm, c.update))
		}
		if c.delete != nil {
			mux.Handle("DELETE "+base+"/{id}", admin(writePerm, c.delete))
		}
	}

	// Content — reads gated by :read, writes by :manage (admin holds both).
	register("subjects", "subjects:read", "subjects:manage", crudSet{a.listSubjects, a.createSubject, a.getSubject, a.updateSubject, a.deleteSubject})
	register("chapters", "chapters:read", "chapters:manage", crudSet{a.listChapters, a.createChapter, a.getChapter, a.updateChapter, a.deleteChapter})
	register("notes", "notes:read", "notes:manage", crudSet{a.listNotes, a.createNote, a.getNote, a.updateNote, a.deleteNote})
	register("questions", "questions:read", "questions:manage", crudSet{a.listQuestions, a.createQuestion, a.getQuestion, a.updateQuestion, a.deleteQuestion})
	register("tests", "tests:read", "tests:manage", crudSet{a.listTests, a.createTest, a.getTest, a.updateTest, a.deleteTest})

	// Users — read + hard delete (delete is super-admin-only via users:delete).
	register("users", "users:read", "users:read", crudSet{list: a.dashListUsers, get: a.dashGetUser})
	mux.Handle("DELETE /v1/admin/users/{id}", admin("users:delete", a.deleteUser))

	// Sessions — list + revoke.
	mux.Handle("GET /v1/admin/sessions", admin("sessions:read", a.dashListSessions))
	mux.Handle("DELETE /v1/admin/sessions/{id}", admin("sessions:revoke", a.dashRevokeSession))

	// Attempts — read + delete (delete is super-admin-only via attempts:delete).
	register("attempts", "attempts:read", "attempts:read", crudSet{list: a.listAttempts, get: a.getAttempt})
	mux.Handle("DELETE /v1/admin/attempts/{id}", admin("attempts:delete", a.deleteAttempt))

	// Battles — read + moderation (force-finish, delete).
	register("battles", "battles:read", "battles:read", crudSet{list: a.listBattles, get: a.getBattle})
	mux.Handle("POST /v1/admin/battles/{id}/finish", admin("battles:manage", a.finishBattle))
	mux.Handle("DELETE /v1/admin/battles/{id}", admin("battles:manage", a.deleteBattle))

	// Follows — read + moderation delete.
	mux.Handle("GET /v1/admin/follows", admin("follows:read", a.listFollows))
	mux.Handle("DELETE /v1/admin/follows/{follower}/{followee}", admin("follows:read", a.deleteFollow))

	// RBAC catalog (read).
	mux.Handle("GET /v1/admin/roles", admin("users:read", a.listRoles))
	mux.Handle("GET /v1/admin/roles/{id}", admin("users:read", a.getRole))
	mux.Handle("GET /v1/admin/permissions", admin("users:read", a.listPermissions))

	// RBAC management (super-admin only — gated by roles:manage, which only
	// super_admin holds; super_admin also bypasses all checks).
	mux.Handle("POST /v1/admin/permissions", admin("roles:manage", a.createPermission))
	mux.Handle("PATCH /v1/admin/permissions/{id}", admin("roles:manage", a.updatePermission))
	mux.Handle("DELETE /v1/admin/permissions/{id}", admin("roles:manage", a.deletePermission))

	mux.Handle("POST /v1/admin/roles", admin("roles:manage", a.createRole))
	mux.Handle("PATCH /v1/admin/roles/{id}", admin("roles:manage", a.updateRole))
	mux.Handle("DELETE /v1/admin/roles/{id}", admin("roles:manage", a.deleteRole))
	mux.Handle("PUT /v1/admin/roles/{id}/permissions", admin("roles:manage", a.setRolePermissions))

	// Assign a user's full role set (super-admin). Single grant/revoke remain at
	// POST/DELETE /v1/admin/users/{id}/roles[/{role}].
	mux.Handle("PUT /v1/admin/users/{id}/roles", admin("roles:manage", a.setUserRoles))
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
