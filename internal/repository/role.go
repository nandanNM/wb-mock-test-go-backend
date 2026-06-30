package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RoleRepository provides access to RBAC tables.
type RoleRepository struct {
	pool *pgxpool.Pool
}

func NewRoleRepository(pool *pgxpool.Pool) *RoleRepository {
	return &RoleRepository{pool: pool}
}

// Role and Permission are dashboard read models.
type Role struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
}

type Permission struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
}

// ListRoles returns all roles with their permission names attached.
func (r *RoleRepository) ListRoles(ctx context.Context) ([]Role, error) {
	rows, err := r.pool.Query(ctx, `SELECT id::text, name, description FROM roles ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Role, 0)
	for rows.Next() {
		var ro Role
		if err := rows.Scan(&ro.ID, &ro.Name, &ro.Description); err != nil {
			return nil, err
		}
		out = append(out, ro)
	}
	return out, rows.Err()
}

// ListPermissions returns all permissions.
func (r *RoleRepository) ListPermissions(ctx context.Context) ([]Permission, error) {
	rows, err := r.pool.Query(ctx, `SELECT id::text, name, description FROM permissions ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Permission, 0)
	for rows.Next() {
		var p Permission
		if err := rows.Scan(&p.ID, &p.Name, &p.Description); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// RoleWithPermissions is a role plus its attached permissions.
type RoleWithPermissions struct {
	Role
	Permissions []Permission `json:"permissions"`
}

// --- Permission CRUD (super-admin) -----------------------------------------

func (r *RoleRepository) CreatePermission(ctx context.Context, name, description string) (Permission, error) {
	var p Permission
	err := r.pool.QueryRow(ctx,
		`INSERT INTO permissions (name, description) VALUES ($1, NULLIF($2,''))
		 RETURNING id::text, name, description`, name, description).
		Scan(&p.ID, &p.Name, &p.Description)
	return p, mapWrite(err)
}

func (r *RoleRepository) UpdatePermission(ctx context.Context, id string, name, description *string) (Permission, error) {
	var p Permission
	err := r.pool.QueryRow(ctx,
		`UPDATE permissions SET name = COALESCE($2, name), description = COALESCE($3, description)
		 WHERE id = $1 RETURNING id::text, name, description`, id, name, description).
		Scan(&p.ID, &p.Name, &p.Description)
	if errors.Is(err, pgx.ErrNoRows) {
		return Permission{}, ErrNotFound
	}
	return p, mapWrite(err)
}

func (r *RoleRepository) DeletePermission(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM permissions WHERE id = $1`, id)
	if err != nil {
		return mapWrite(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- Role CRUD (super-admin) -----------------------------------------------

func (r *RoleRepository) CreateRole(ctx context.Context, name, description string) (Role, error) {
	var ro Role
	err := r.pool.QueryRow(ctx,
		`INSERT INTO roles (name, description) VALUES ($1, NULLIF($2,''))
		 RETURNING id::text, name, description`, name, description).
		Scan(&ro.ID, &ro.Name, &ro.Description)
	return ro, mapWrite(err)
}

func (r *RoleRepository) UpdateRole(ctx context.Context, id string, name, description *string) (Role, error) {
	var ro Role
	err := r.pool.QueryRow(ctx,
		`UPDATE roles SET name = COALESCE($2, name), description = COALESCE($3, description)
		 WHERE id = $1 RETURNING id::text, name, description`, id, name, description).
		Scan(&ro.ID, &ro.Name, &ro.Description)
	if errors.Is(err, pgx.ErrNoRows) {
		return Role{}, ErrNotFound
	}
	return ro, mapWrite(err)
}

func (r *RoleRepository) DeleteRole(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM roles WHERE id = $1`, id)
	if err != nil {
		return mapWrite(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetRoleWithPermissions returns a role and its attached permissions.
func (r *RoleRepository) GetRoleWithPermissions(ctx context.Context, id string) (RoleWithPermissions, error) {
	var out RoleWithPermissions
	err := r.pool.QueryRow(ctx, `SELECT id::text, name, description FROM roles WHERE id = $1`, id).
		Scan(&out.ID, &out.Name, &out.Description)
	if errors.Is(err, pgx.ErrNoRows) {
		return RoleWithPermissions{}, ErrNotFound
	}
	if err != nil {
		return RoleWithPermissions{}, err
	}
	rows, err := r.pool.Query(ctx,
		`SELECT p.id::text, p.name, p.description
		 FROM role_permissions rp JOIN permissions p ON p.id = rp.permission_id
		 WHERE rp.role_id = $1 ORDER BY p.name`, id)
	if err != nil {
		return RoleWithPermissions{}, err
	}
	defer rows.Close()
	out.Permissions = make([]Permission, 0)
	for rows.Next() {
		var p Permission
		if err := rows.Scan(&p.ID, &p.Name, &p.Description); err != nil {
			return RoleWithPermissions{}, err
		}
		out.Permissions = append(out.Permissions, p)
	}
	return out, rows.Err()
}

// SetRolePermissions replaces a role's permission set with the given permission
// IDs (atomic). Returns ErrNotFound if the role doesn't exist. Unknown
// permission IDs are ignored.
func (r *RoleRepository) SetRolePermissions(ctx context.Context, roleID string, permissionIDs []string) error {
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM roles WHERE id=$1)`, roleID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		if _, err := tx.Exec(ctx, `DELETE FROM role_permissions WHERE role_id = $1`, roleID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO role_permissions (role_id, permission_id)
			 SELECT $1, id FROM permissions WHERE id = ANY($2::uuid[])`, roleID, permissionIDs)
		return err
	})
}

// SetUserRoles replaces a user's roles with the given role names (atomic).
// Unknown role names are ignored.
func (r *RoleRepository) SetUserRoles(ctx context.Context, userID string, roleNames []string) error {
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM user_roles WHERE user_id = $1`, userID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO user_roles (user_id, role_id)
			 SELECT $1, id FROM roles WHERE name = ANY($2::text[])`, userID, roleNames)
		return err
	})
}

// IsSystemRole reports whether a role name is a protected built-in.
func IsSystemRole(name string) bool {
	switch name {
	case "super_admin", "admin", "user", "teacher":
		return true
	default:
		return false
	}
}

// RolesForUser returns the user's role names.
func (r *RoleRepository) RolesForUser(ctx context.Context, userID string) ([]string, error) {
	const q = `
		SELECT ro.name
		FROM user_roles ur JOIN roles ro ON ro.id = ur.role_id
		WHERE ur.user_id = $1
		ORDER BY ro.name`
	return r.scanStrings(ctx, q, userID)
}

// PermissionsForRoles expands a set of role names into the union of their
// permission names.
func (r *RoleRepository) PermissionsForRoles(ctx context.Context, roleNames []string) ([]string, error) {
	if len(roleNames) == 0 {
		return []string{}, nil
	}
	const q = `
		SELECT DISTINCT p.name
		FROM roles ro
		JOIN role_permissions rp ON rp.role_id = ro.id
		JOIN permissions p ON p.id = rp.permission_id
		WHERE ro.name = ANY($1)
		ORDER BY p.name`
	return r.scanStrings(ctx, q, roleNames)
}

// AssignRole grants a role to a user (idempotent). Returns ErrNotFound if the
// role name does not exist.
func (r *RoleRepository) AssignRole(ctx context.Context, userID, roleName, grantedBy string) error {
	const q = `
		INSERT INTO user_roles (user_id, role_id, granted_by)
		SELECT $1, id, NULLIF($3,'')::uuid FROM roles WHERE name = $2
		ON CONFLICT (user_id, role_id) DO NOTHING`
	tag, err := r.pool.Exec(ctx, q, userID, roleName, grantedBy)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// Either the role name is unknown or the grant already existed. Confirm
		// the role exists to distinguish.
		var exists bool
		if e := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM roles WHERE name=$1)`, roleName).Scan(&exists); e != nil {
			return e
		}
		if !exists {
			return ErrNotFound
		}
	}
	return nil
}

// RevokeRole removes a role from a user.
func (r *RoleRepository) RevokeRole(ctx context.Context, userID, roleName string) error {
	const q = `
		DELETE FROM user_roles
		WHERE user_id = $1 AND role_id = (SELECT id FROM roles WHERE name = $2)`
	_, err := r.pool.Exec(ctx, q, userID, roleName)
	return err
}

func (r *RoleRepository) scanStrings(ctx context.Context, q string, args ...any) ([]string, error) {
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]string, 0)
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
