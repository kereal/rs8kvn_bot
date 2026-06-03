-- 004_add_unique_referrer_tg_id.down.sql
-- Drops the unique index created by 004.
-- Note: the deduplication (deletion of duplicate codes) performed in the up
-- migration is intentionally NOT reversed.

DROP INDEX IF EXISTS idx_invites_referrer_unique;
