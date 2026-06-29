-- 0003_auth_sessions: one row per logged-in device.
--
-- Refresh tokens are stored ONLY as SHA-256 hashes. Rotation is an atomic
-- compare-and-swap on refresh_token_hash; prev_refresh_token_hash + rotated_at
-- support a short grace window so concurrent refreshes from the same device are
-- not mistaken for token reuse.

CREATE TABLE IF NOT EXISTS sessions (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id                UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    refresh_token_hash     TEXT NOT NULL,
    prev_refresh_token_hash TEXT,
    token_generation       INT  NOT NULL DEFAULT 1,
    user_agent             TEXT,
    ip                     INET,
    device_label           TEXT,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    rotated_at             TIMESTAMPTZ,
    expires_at             TIMESTAMPTZ NOT NULL,
    revoked_at             TIMESTAMPTZ,
    revoked_reason         TEXT
);

-- A refresh token hash is unique among active (non-revoked) sessions.
CREATE UNIQUE INDEX IF NOT EXISTS uq_sessions_refresh_hash_active
    ON sessions (refresh_token_hash) WHERE revoked_at IS NULL;

-- Fast device listing + bulk revoke for a user.
CREATE INDEX IF NOT EXISTS idx_sessions_user_active
    ON sessions (user_id) WHERE revoked_at IS NULL;
