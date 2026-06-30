-- 0013_revoke_admin_privileged: make privileged actions super_admin-only.
--
-- Migration 0004 blanket-granted the admin role every permission that existed
-- at the time, which inadvertently included roles:manage. Role/permission
-- management and hard deletes must be super_admin-only, so revoke these from
-- the admin role. (super_admin keeps them and bypasses checks regardless.)

DELETE FROM role_permissions rp
USING roles r, permissions p
WHERE rp.role_id = r.id
  AND rp.permission_id = p.id
  AND r.name = 'admin'
  AND p.name IN ('roles:manage', 'users:delete', 'attempts:delete');
