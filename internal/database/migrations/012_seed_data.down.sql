UPDATE subscriptions SET plan_id = NULL WHERE plan_id IN (1, 2);
DELETE FROM plan_sources WHERE plan_id IN (1, 2) AND source_id = 1;
DELETE FROM plans WHERE id IN (1, 2);
DELETE FROM sources WHERE id = 1;
