-- 0005: drop per-feed sharing.
--
-- Public/private classification now lives only on collections
-- (visibility_status). Individual feeds can no longer be shared directly;
-- they are shared by importing or sharing a collection.

ALTER TABLE feeds DROP COLUMN share_status;
ALTER TABLE feeds DROP COLUMN share_requested;

DROP INDEX IF EXISTS feeds_shared_idx;
