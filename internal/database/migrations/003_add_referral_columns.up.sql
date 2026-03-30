-- Add referral columns to subscriptions table

ALTER TABLE subscriptions ADD COLUMN invite_code VARCHAR(16);
ALTER TABLE subscriptions ADD COLUMN is_trial INTEGER DEFAULT 0;
ALTER TABLE subscriptions ADD COLUMN referred_by BIGINT;

CREATE INDEX IF NOT EXISTS idx_subscriptions_invite_code ON subscriptions(invite_code);
CREATE INDEX IF NOT EXISTS idx_subscriptions_is_trial ON subscriptions(is_trial);
CREATE INDEX IF NOT EXISTS idx_subscriptions_referred_by ON subscriptions(referred_by);
