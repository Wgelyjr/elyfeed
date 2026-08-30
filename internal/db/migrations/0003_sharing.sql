-- 0003: sharing.
--
-- Adds the community feed directory (feeds can be private, shared, or pending
-- admin review), the admin-curated recommended feed catalog shown during
-- onboarding, and shareable collections (a token that lets others import a
-- collection's feed URLs into their own account).

-- Sharing state on feeds. share_status is the current, visible state;
-- share_requested is the owner's intended target while a change awaits admin
-- review (only set when share_status = 'pending').
ALTER TABLE feeds ADD COLUMN share_status TEXT NOT NULL DEFAULT 'private'
    CHECK (share_status IN ('private', 'pending', 'shared'));
ALTER TABLE feeds ADD COLUMN share_requested TEXT
    CHECK (share_requested IS NULL OR share_requested IN ('private', 'shared'));

-- Speeds up the shared-feeds directory listing.
CREATE INDEX feeds_shared_idx ON feeds (title) WHERE share_status = 'shared';

-- Admin-curated starter catalog surfaced during onboarding.
CREATE TABLE recommended_feeds (
    id         BIGSERIAL PRIMARY KEY,
    url        TEXT NOT NULL UNIQUE,
    title      TEXT NOT NULL DEFAULT '',
    site_url   TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Shareable collections: a token that resolves to a collection's name and its
-- feed URLs. One token per collection; regenerating replaces the old token.
CREATE TABLE collection_shares (
    id            BIGSERIAL PRIMARY KEY,
    collection_id BIGINT NOT NULL UNIQUE REFERENCES collections(id) ON DELETE CASCADE,
    token         TEXT NOT NULL UNIQUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
