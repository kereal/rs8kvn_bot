-- +migrate Up
-- Copy x_ui_host data to subscription_id (extract last part from URL)
UPDATE subscriptions SET subscription_id = 
    SUBSTR(subscription_url, INSTR(subscription_url, '/s/') + 3)
WHERE subscription_url LIKE '%/s/%';
-- Drop old column
ALTER TABLE subscriptions DROP COLUMN x_ui_host;

-- +migrate Down
-- No down migration needed
