-- 0011_dashboard_rbac: super_admin role + dashboard permissions.
--
-- super_admin bypasses permission checks in code (access to everything); the
-- explicit grants below are for completeness/visibility. admin gets a
-- production subset (no role management, no hard deletes, no privilege grants).

INSERT INTO roles (name, description) VALUES
  ('super_admin', 'Unrestricted access to the entire system')
ON CONFLICT (name) DO NOTHING;

INSERT INTO permissions (name, description) VALUES
  ('users:delete',     'Hard-delete user accounts'),
  ('attempts:read',    'View test attempts'),
  ('attempts:delete',  'Delete test attempts'),
  ('battles:read',     'View battles'),
  ('battles:manage',   'Force-finish or delete battles'),
  ('follows:read',     'View follow relationships'),
  ('sessions:read',    'View user sessions'),
  ('sessions:revoke',  'Revoke user sessions')
ON CONFLICT (name) DO NOTHING;

-- super_admin: every permission that exists (now and the older ones).
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p
WHERE r.name = 'super_admin'
ON CONFLICT DO NOTHING;

-- admin: production operations, minus privilege/role management and hard deletes.
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p
WHERE r.name = 'admin'
  AND p.name IN (
    'subjects:manage', 'chapters:manage', 'notes:manage',
    'questions:manage', 'tests:manage', 'tests:publish',
    'users:read', 'users:ban',
    'attempts:read', 'battles:read', 'battles:manage',
    'follows:read', 'sessions:read', 'sessions:revoke',
    'audit:read'
  )
ON CONFLICT DO NOTHING;
