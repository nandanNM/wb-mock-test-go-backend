package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RoleRepository provides access to RBAC tables.
type RoleRepository struct {
	pool *pgxpool.Pool
}

func NewRoleRepository(pool *pgxpool.Pool) *RoleRepository {
	return &RoleRepository{pool: pool}
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
