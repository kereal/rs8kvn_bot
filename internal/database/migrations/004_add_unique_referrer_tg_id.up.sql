-- 004_add_unique_referrer_tg_id.up.sql
-- Safely adds the unique constraint on referrer_tg_id.
--
-- IMPORTANT (for legacy databases):
-- Before this migration was fixed, many production DBs accumulated multiple
-- invite codes per referrer_tg_id (because 004 was never applied due to the
-- old runMigrations hack that forced version=3 and returned early).
--
-- This version of 004 is safe: it first removes all duplicates (keeping only
-- the oldest code per referrer), THEN creates the unique index.
--
-- This must be done in 004 (not in a later migration) because golang-migrate
-- runs files in order, and 005 would never be reached if 004 already crashed.

-- Step 1: Remove duplicates - keep only the oldest code per referrer_tg_id
-- (by created_at, then code as tie-breaker).
DELETE FROM invites
WHERE rowid NOT IN (
    SELECT rowid
    FROM (
        SELECT rowid,
               ROW_NUMBER() OVER (
                   PARTITION BY referrer_tg_id
                   ORDER BY created_at ASC, code ASC
               ) AS rn
        FROM invites
    ) ranked
    WHERE rn = 1
);

-- Step 2: Now it is safe to create the unique index.
CREATE UNIQUE INDEX IF NOT EXISTS idx_invites_referrer_unique
    ON invites(referrer_tg_id);
