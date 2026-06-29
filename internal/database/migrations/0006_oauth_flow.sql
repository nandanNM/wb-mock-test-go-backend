-- 0006_oauth_flow: short-lived state for the OAuth Authorization Code + PKCE flow.
--
-- One row per in-flight login. Holds the CSRF `state` and the PKCE
-- `code_verifier` between /start and /callback. Deleted on callback; expired
-- rows are swept opportunistically.

CREATE TABLE IF NOT EXISTS oauth_login_flow (
    state         TEXT PRIMARY KEY,
    provider      TEXT NOT NULL DEFAULT 'google',
    code_verifier TEXT NOT NULL,
    redirect_uri  TEXT,
    client_type   TEXT,                  -- 'web' | 'native' (token delivery hint)
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_oauth_flow_expires ON oauth_login_flow (expires_at);
