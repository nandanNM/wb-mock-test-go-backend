-- 0001_init: initial schema.
-- Migrations are applied in filename order, each inside its own transaction.
--
-- gen_random_uuid() is built into PostgreSQL core (>= 13), so no extension is
-- required — this runs as-is on Neon, Supabase, RDS, etc.

CREATE TABLE IF NOT EXISTS users (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT        NOT NULL,
    email      TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Case-insensitive unique email without needing the citext extension.
CREATE UNIQUE INDEX IF NOT EXISTS uq_users_email_lower ON users (lower(email));
CREATE INDEX IF NOT EXISTS idx_users_created_at ON users (created_at DESC);
