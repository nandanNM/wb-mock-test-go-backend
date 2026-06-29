-- 0008_quizly_users_rbac: profile columns for rankings/streaks/handle, plus a
-- content-authoring role wired into the EXISTING RBAC tables.
--
-- Integration notes vs. the original Quizly draft:
--   * No denormalized users.role column — authorization reuses roles/permissions.
--   * FKs to users are UUID (this app's users.id is UUID, not BIGINT).
--   * The leaderboard view maps users.name -> display_name (see 0010).

-- Denormalized profile/ranking fields (kept in sync by the app on attempt completion).
ALTER TABLE users ADD COLUMN IF NOT EXISTS handle         VARCHAR(30);
ALTER TABLE users ADD COLUMN IF NOT EXISTS total_points   BIGINT NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN IF NOT EXISTS current_streak INT    NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN IF NOT EXISTS longest_streak INT    NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN IF NOT EXISTS last_active_on DATE;

CREATE UNIQUE INDEX IF NOT EXISTS uq_users_handle ON users (lower(handle)) WHERE handle IS NOT NULL;
CREATE INDEX        IF NOT EXISTS idx_users_points ON users (total_points DESC);

-- Content-authoring role + permissions, integrated with the existing RBAC.
INSERT INTO roles (name, description) VALUES
  ('teacher', 'Can author content, questions and tests')
ON CONFLICT (name) DO NOTHING;

INSERT INTO permissions (name, description) VALUES
  ('subjects:manage',  'Create/update subjects'),
  ('chapters:manage',  'Create/update chapters'),
  ('notes:manage',     'Create/update chapter notes'),
  ('questions:manage', 'Create/update questions and options'),
  ('tests:manage',     'Create/update tests'),
  ('tests:publish',    'Publish or unpublish tests')
ON CONFLICT (name) DO NOTHING;

-- Grant the content permissions to teacher AND admin. (admin's blanket grant in
-- migration 0004 only covered the permissions that existed at that time, so new
-- permissions must be granted explicitly.)
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r CROSS JOIN permissions p
WHERE r.name IN ('teacher', 'admin')
  AND p.name IN ('subjects:manage', 'chapters:manage', 'notes:manage',
                 'questions:manage', 'tests:manage', 'tests:publish')
ON CONFLICT DO NOTHING;
