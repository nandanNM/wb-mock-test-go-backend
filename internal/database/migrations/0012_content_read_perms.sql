-- 0012_content_read_perms: split content reads from writes.
--
-- Content list/get endpoints are now gated by ':read' permissions (writes stay
-- on ':manage'), enabling a future view-only admin tier. The admin role gets
-- both; super_admin gets everything (and bypasses checks anyway).

INSERT INTO permissions (name, description) VALUES
  ('subjects:read',  'View subjects'),
  ('chapters:read',  'View chapters'),
  ('notes:read',     'View chapter notes'),
  ('questions:read', 'View questions and options'),
  ('tests:read',     'View tests')
ON CONFLICT (name) DO NOTHING;

-- super_admin: keep its blanket grant current.
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p
WHERE r.name = 'super_admin'
ON CONFLICT DO NOTHING;

-- admin: gets the read permissions (already holds the matching :manage).
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p
WHERE r.name = 'admin'
  AND p.name IN ('subjects:read', 'chapters:read', 'notes:read', 'questions:read', 'tests:read')
ON CONFLICT DO NOTHING;
