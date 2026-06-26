-- Migration: 025_add_retry_check_constraint
-- Description: Инвариант №5: если retry_count > 0, то retry_at не может быть NULL.

ALTER TABLE subscription_nodes ADD CHECK (
    retry_count = 0 OR retry_at IS NOT NULL
);
