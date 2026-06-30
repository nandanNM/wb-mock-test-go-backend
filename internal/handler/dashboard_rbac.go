package handler

import (
	"net/http"
	"strings"

	"backend/internal/httpx"
	"backend/internal/repository"
)

// The handlers below are the Super Admin RBAC management module. They are
// mounted under the `roles:manage` permission (super-admin only) except the
// read endpoints. After any mutation they invalidate the RBAC permission cache
// so changes take effect immediately, and write an audit event.

// --- Permissions -----------------------------------------------------------

type permissionReq struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (a *API) createPermission(w http.ResponseWriter, r *http.Request) error {
	var req permissionReq
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.Name) == "" {
		return httpx.ErrValidation.WithDetails(map[string]any{"name": "is required"})
	}
	p, err := a.roles.CreatePermission(r.Context(), strings.TrimSpace(req.Name), req.Description)
	if err != nil {
		return writeRepoError(err, "name", req.Name)
	}
	a.rbac.InvalidateAll()
	a.auditDash(r, "dashboard.permission.created", map[string]any{"permission": p.Name})
	httpx.JSON(w, http.StatusCreated, p)
	return nil
}

type permissionUpdateReq struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

func (a *API) updatePermission(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	var req permissionUpdateReq
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	p, err := a.roles.UpdatePermission(r.Context(), id, req.Name, req.Description)
	if err != nil {
		return writeRepoError(err, "id", id)
	}
	a.rbac.InvalidateAll()
	a.auditDash(r, "dashboard.permission.updated", map[string]any{"permission_id": id})
	httpx.JSON(w, http.StatusOK, p)
	return nil
}

func (a *API) deletePermission(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	if err := a.roles.DeletePermission(r.Context(), id); err != nil {
		return writeRepoError(err, "id", id)
	}
	a.rbac.InvalidateAll()
	a.auditDash(r, "dashboard.permission.deleted", map[string]any{"permission_id": id})
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "deleted", "id": id})
	return nil
}

// --- Roles -----------------------------------------------------------------

type roleReqBody struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (a *API) createRole(w http.ResponseWriter, r *http.Request) error {
	var req roleReqBody
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.Name) == "" {
		return httpx.ErrValidation.WithDetails(map[string]any{"name": "is required"})
	}
	ro, err := a.roles.CreateRole(r.Context(), strings.TrimSpace(req.Name), req.Description)
	if err != nil {
		return writeRepoError(err, "name", req.Name)
	}
	a.auditDash(r, "dashboard.role.created", map[string]any{"role": ro.Name})
	httpx.JSON(w, http.StatusCreated, ro)
	return nil
}

func (a *API) getRole(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	role, err := a.roles.GetRoleWithPermissions(r.Context(), id)
	if err != nil {
		return writeRepoError(err, "id", id)
	}
	httpx.JSON(w, http.StatusOK, role)
	return nil
}

type roleUpdateReq struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

func (a *API) updateRole(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	var req roleUpdateReq
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	// Don't allow renaming a protected system role (would break code that
	// references it by name, e.g. the super_admin bypass).
	if cur, err := a.roles.GetRoleWithPermissions(r.Context(), id); err == nil &&
		repository.IsSystemRole(cur.Name) && req.Name != nil && *req.Name != cur.Name {
		return httpx.NewAPIError(http.StatusConflict, "system_role", "System roles cannot be renamed.")
	}
	ro, err := a.roles.UpdateRole(r.Context(), id, req.Name, req.Description)
	if err != nil {
		return writeRepoError(err, "id", id)
	}
	a.rbac.InvalidateAll()
	a.auditDash(r, "dashboard.role.updated", map[string]any{"role_id": id})
	httpx.JSON(w, http.StatusOK, ro)
	return nil
}

func (a *API) deleteRole(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	cur, err := a.roles.GetRoleWithPermissions(r.Context(), id)
	if err != nil {
		return writeRepoError(err, "id", id)
	}
	if repository.IsSystemRole(cur.Name) {
		return httpx.NewAPIError(http.StatusConflict, "system_role", "System roles cannot be deleted.")
	}
	if err := a.roles.DeleteRole(r.Context(), id); err != nil {
		return writeRepoError(err, "id", id)
	}
	a.rbac.InvalidateAll()
	a.auditDash(r, "dashboard.role.deleted", map[string]any{"role": cur.Name})
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "deleted", "id": id})
	return nil
}

// setRolePermissions replaces a role's permission set.
type rolePermsReq struct {
	PermissionIDs []string `json:"permission_ids"`
}

func (a *API) setRolePermissions(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	var req rolePermsReq
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	if err := a.roles.SetRolePermissions(r.Context(), id, req.PermissionIDs); err != nil {
		return writeRepoError(err, "id", id)
	}
	a.rbac.InvalidateAll()
	a.auditDash(r, "dashboard.role.permissions_set", map[string]any{"role_id": id, "count": len(req.PermissionIDs)})
	role, err := a.roles.GetRoleWithPermissions(r.Context(), id)
	if err != nil {
		return writeRepoError(err, "id", id)
	}
	httpx.JSON(w, http.StatusOK, role)
	return nil
}

// setUserRoles replaces a user's role set (super-admin).
type userRolesReq struct {
	Roles []string `json:"roles"`
}

func (a *API) setUserRoles(w http.ResponseWriter, r *http.Request) error {
	actor, _ := principalUserID(r)
	userID := r.PathValue("id")
	var req userRolesReq
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	if err := a.roles.SetUserRoles(r.Context(), userID, req.Roles); err != nil {
		return writeRepoError(err, "id", userID)
	}
	// The user's tokens carry roles until they expire/refresh; invalidate the
	// status cache so the next refresh re-reads roles promptly.
	a.auth.InvalidateUserCache(userID)
	a.auditDash(r, "dashboard.user.roles_set", map[string]any{"user_id": userID, "roles": req.Roles, "actor_id": actor})
	roles, err := a.roles.RolesForUser(r.Context(), userID)
	if err != nil {
		return httpx.Wrap(httpx.ErrInternal, err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"user_id": userID, "roles": roles})
	return nil
}
