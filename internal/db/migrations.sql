CREATE TABLE IF NOT EXISTS feeds (
    id           BIGSERIAL PRIMARY KEY,
    url          TEXT NOT NULL UNIQUE,
    title        TEXT NOT NULL DEFAULT '',
    site_url     TEXT NOT NULL DEFAULT '',
    last_fetched TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS items (
    id           BIGSERIAL PRIMARY KEY,
    feed_id      BIGINT NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
    guid         TEXT NOT NULL,
    title        TEXT NOT NULL DEFAULT '',
    link         TEXT NOT NULL DEFAULT '',
    content      TEXT NOT NULL DEFAULT '',
    author       TEXT NOT NULL DEFAULT '',
    published_at TIMESTAMPTZ,
    fetched_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    read         BOOLEAN NOT NULL DEFAULT false,
    UNIQUE (feed_id, guid)
);

CREATE INDEX IF NOT EXISTS items_feed_id_idx ON items (feed_id);
CREATE INDEX IF NOT EXISTS items_published_at_idx ON items (published_at DESC);
CREATE INDEX IF NOT EXISTS items_read_idx ON items (read);
