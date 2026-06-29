-- 0002_auth_identities: external identity providers + user account status.
--
-- `identities` decouples *how a user authenticates* (Google now; phone/OTP/TOTP
-- later) from the `users` row. Adding a future provider = new rows here + a new
-- IdentityProvider implementation; no schema rewrite.

-- Reusable trigger to keep updated_at fresh on row updates.
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Account status + verification flags on the existing users table.
ALTER TABLE users ADD COLUMN IF NOT EXISTS status        TEXT    NOT NULL DEFAULT 'active';
ALTER TABLE users ADD COLUMN IF NOT EXISTS status_reason TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS email_verified BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE users ADD COLUMN IF NOT EXISTS perm_version  INT     NOT NULL DEFAULT 1;

-- status ∈ {active, suspended, banned}
ALTER TABLE users DROP CONSTRAINT IF EXISTS chk_users_status;
ALTER TABLE users ADD  CONSTRAINT chk_users_status CHECK (status IN ('active', 'suspended', 'banned'));

DROP TRIGGER IF EXISTS trg_users_updated_at ON users;
CREATE TRIGGER trg_users_updated_at BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE IF NOT EXISTS identities (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider         TEXT NOT NULL,          -- 'google' | 'phone' | 'password' | ...
    provider_subject TEXT NOT NULL,          -- Google `sub`, phone number, etc.
    email            TEXT,
    email_verified   BOOLEAN NOT NULL DEFAULT false,
    data             JSONB   NOT NULL DEFAULT '{}'::jsonb,  -- non-secret provider profile only
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider, provider_subject)
);

CREATE INDEX IF NOT EXISTS idx_identities_user_id ON identities (user_id);

DROP TRIGGER IF EXISTS trg_identities_updated_at ON identities;
CREATE TRIGGER trg_identities_updated_at BEFORE UPDATE ON identities
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
