// Package rbac provides role/permission authorization checks and middleware.
//
// Access tokens carry the caller's role names; permissions are expanded from
// roles here (with a short-lived cache) so a role's permission set can change
// without re-issuing tokens.
package rbac

import (
	"context"
	"net/http"
	"sync"
	"time"

	"backend/internal/middleware"
	"backend/internal/repository"
)

// Service expands roles into permissions, caching per-role results.
type Service struct {
	roles *repository.RoleRepository
	ttl   time.Duration

	mu    sync.Mutex
	cache map[string]cacheEntry // role name -> permissions
}

type cacheEntry struct {
	perms []string
	exp   time.Time
}

// NewService builds an RBAC service with the given per-role cache TTL.
func NewService(roles *repository.RoleRepository, ttl time.Duration) *Service {
	if ttl <= 0 {
		ttl = time.Minute
	}
	return &Service{roles: roles, ttl: ttl, cache: make(map[string]cacheEntry)}
}

// HasRole reports whether any of the caller's roles matches name.
func HasRole(roles []string, name string) bool {
	for _, r := range roles {
		if r == name {
			return true
		}
	}
	return false
}

// Can reports whether the given roles grant the named permission.
func (s *Service) Can(ctx context.Context, roles []string, permission string) (bool, error) {
	for _, role := range roles {
		perms, err := s.permsForRole(ctx, role)
		if err != nil {
			return false, err
		}
		for _, p := range perms {
			if p == permission {
				return true, nil
			}
		}
	}
	return false, nil
}

func (s *Service) permsForRole(ctx context.Context, role string) ([]string, error) {
	now := time.Now()
	s.mu.Lock()
	if e, ok := s.cache[role]; ok && now.Before(e.exp) {
		s.mu.Unlock()
		return e.perms, nil
	}
	s.mu.Unlock()

	perms, err := s.roles.PermissionsForRoles(ctx, []string{role})
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.cache[role] = cacheEntry{perms: perms, exp: now.Add(s.ttl)}
	s.mu.Unlock()
	return perms, nil
}

// RequireRole is middleware that allows only callers holding the named role.
// Must run after authentication (a Principal must be in the context).
func RequireRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := middleware.PrincipalFromContext(r.Context())
			if !ok {
				middleware.WriteError(w, r, http.StatusUnauthorized, "unauthorized", "Authentication is required.")
				return
			}
			if !HasRole(p.Roles, role) {
				middleware.WriteError(w, r, http.StatusForbidden, "forbidden", "Insufficient role.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequirePermission is middleware that allows only callers whose roles grant the
// named permission. Must run after authentication.
func (s *Service) RequirePermission(permission string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := middleware.PrincipalFromContext(r.Context())
			if !ok {
				middleware.WriteError(w, r, http.StatusUnauthorized, "unauthorized", "Authentication is required.")
				return
			}
			ok, err := s.Can(r.Context(), p.Roles, permission)
			if err != nil {
				middleware.WriteError(w, r, http.StatusServiceUnavailable, "authz_unavailable", "Could not evaluate permissions.")
				return
			}
			if !ok {
				middleware.WriteError(w, r, http.StatusForbidden, "forbidden", "Insufficient permission.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
