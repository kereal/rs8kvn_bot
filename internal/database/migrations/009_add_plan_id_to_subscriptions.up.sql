ALTER TABLE subscriptions ADD COLUMN plan_id INTEGER REFERENCES plans(id);
