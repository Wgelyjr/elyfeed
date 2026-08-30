-- 0004: public collections.
--
-- Lets collections be private, public, or pending admin review (mirroring feed
-- sharing). Public collections appear in a community directory that other
-- users can browse and import their feeds from with one click.

-- Visibility state on collections. visibility_status is the current, visible
-- state; visibility_requested is the owner's intended target while a change
-- awaits admin review (only set when visibility_status = 'pending').
ALTER TABLE collections ADD COLUMN visibility_status TEXT NOT NULL DEFAULT 'private'
    CHECK (visibility_status IN ('private', 'pending', 'public'));
ALTER TABLE collections ADD COLUMN visibility_requested TEXT
    CHECK (visibility_requested IS NULL OR visibility_requested IN ('private', 'public'));

-- Speeds up the public-collections directory listing.
CREATE INDEX collections_public_idx ON collections (name) WHERE visibility_status = 'public';
