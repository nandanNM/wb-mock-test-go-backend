-- 0005_auth_audit_log: append-only record of authentication events.
--
-- Written on login success/failure, token refresh, reuse detection, logout,
-- logout-all, ban/suspend/reinstate, and role changes. request_id correlates an
-- event with the request log line.

CREATE TABLE IF NOT EXISTS auth_audit_log (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    event_type  TEXT NOT NULL,            -- 'login_success', 'token_reuse_detected', ...
    user_id     UUID REFERENCES users(id) ON DELETE SET NULL,
    session_id  UUID,                     -- not FK: survives session deletion
    ip          INET,
    user_agent  TEXT,
    request_id  TEXT,
    detail      JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX IF NOT EXISTS idx_audit_user_time ON auth_audit_log (user_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_type_time ON auth_audit_log (event_type, occurred_at DESC);
