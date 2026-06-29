-- 0004_rbac: role-based access control.
--
-- users --< user_roles >-- roles --< role_permissions >-- permissions
-- Middleware can require a role OR a specific permission. Seeded with 'user'
-- and 'admin'; admin is granted all permissions.

CREATE TABLE IF NOT EXISTS roles (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    description TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS permissions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,   -- e.g. 'users:read', 'users:ban', 'audit:read'
    description TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS role_permissions (
    role_id       UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission_id UUID NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission_id)
);

CREATE TABLE IF NOT EXISTS user_roles (
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id     UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    granted_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    granted_by  UUID REFERENCES users(id),
    PRIMARY KEY (user_id, role_id)
);

CREATE INDEX IF NOT EXISTS idx_user_roles_user ON user_roles (user_id);

-- Seed roles.
INSERT INTO roles (name, description) VALUES
    ('user',  'Standard authenticated user'),
    ('admin', 'Full administrative access')
ON CONFLICT (name) DO NOTHING;

-- Seed a starter permission set.
INSERT INTO permissions (name, description) VALUES
    ('users:read',   'List and view users'),
    ('users:ban',    'Ban, suspend or reinstate users'),
    ('roles:manage', 'Grant or revoke user roles'),
    ('audit:read',   'View authentication audit log')
ON CONFLICT (name) DO NOTHING;

-- Grant all permissions to admin.
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p
WHERE r.name = 'admin'
ON CONFLICT DO NOTHING;
