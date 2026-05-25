-- 005_cleanup_duplicate_invites.down.sql
-- Reversible part: drop the unique index.
-- The data cleanup (deletion of duplicate codes) is intentionally NOT reversed —
-- once old codes are gone they stay gone. Re-creating duplicates would re-introduce
-- the original bug.

DROP INDEX IF EXISTS idx_invites_referrer_unique;
