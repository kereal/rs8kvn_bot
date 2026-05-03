-- Add plan column to subscriptions for user group/tariff tracking
-- Default is 'free', allowed values: free, basic, premium, vip

ALTER TABLE subscriptions ADD COLUMN plan TEXT NOT NULL DEFAULT 'free';

CREATE INDEX IF NOT EXISTS idx_subscriptions_plan ON subscriptions(plan);
