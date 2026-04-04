-- Add unique constraint on referrer_tg_id to prevent duplicate invites
-- This enables atomic INSERT ... ON CONFLICT DO NOTHING for GetOrCreateInvite

CREATE UNIQUE INDEX IF NOT EXISTS idx_invites_referrer_unique ON invites(referrer_tg_id);
