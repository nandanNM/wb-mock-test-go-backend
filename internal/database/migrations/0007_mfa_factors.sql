-- 0007_mfa_factors: second-factor store (SEAM — table only, not yet wired).
--
-- Exists ahead of the TOTP/SMS 2FA feature so secrets have a dedicated,
-- encrypted home from day one (never stored in identities.data). The login
-- pipeline already models an 'mfa_required' state; wiring this is additive.

CREATE TABLE IF NOT EXISTS mfa_factors (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type             TEXT NOT NULL,            -- 'totp' | 'sms'
    secret_encrypted BYTEA,                    -- app-layer encrypted; never plaintext
    label            TEXT,
    confirmed_at     TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_mfa_factors_user ON mfa_factors (user_id);
