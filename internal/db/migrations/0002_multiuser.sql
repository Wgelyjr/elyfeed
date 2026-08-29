-- 0002: multi-user support.
--
-- Adds user accounts, sessions, and email verification tokens, and scopes
-- feeds/collections to a user. user_id = 0 marks legacy (pre-multi-user)
-- rows; they are reassigned to the first user that activates.

CREATE TABLE users (
    id             BIGSERIAL PRIMARY KEY,
    email          TEXT NOT NULL UNIQUE,
    display_name   TEXT NOT NULL DEFAULT '',
    password_hash  TEXT,
    role           TEXT NOT NULL DEFAULT 'user',
    email_verified BOOLEAN NOT NULL DEFAULT false,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

CREATE TABLE email_verifications (
    id         BIGSERIAL PRIMARY KEY,
    email      TEXT NOT NULL,
    token_hash TEXT NOT NULL,
    purpose    TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX email_verifications_email_idx ON email_verifications (email);

-- Scope feeds to a user. The global unique(url) becomes unique(user_id, url)
-- so different users may subscribe to the same feed URL. In 0001 the
-- uniqueness is an inline UNIQUE constraint, whose backing index cannot be
-- dropped directly; drop the constraint first (which drops its index) and
-- then any plain index of the same name.
ALTER TABLE feeds ADD COLUMN user_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE feeds DROP CONSTRAINT IF EXISTS feeds_url_key;
DROP INDEX IF EXISTS feeds_url_key;
CREATE UNIQUE INDEX feeds_user_url_uq ON feeds (user_id, url);

-- Scope collections to a user; names are unique per user.
ALTER TABLE collections ADD COLUMN user_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE collections DROP CONSTRAINT IF EXISTS collections_name_key;
DROP INDEX IF EXISTS collections_name_key;
CREATE UNIQUE INDEX collections_user_name_uq ON collections (user_id, name);
