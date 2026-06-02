INSERT OR IGNORE INTO sources (id, name, active, x_ui_host, x_ui_api_token, x_ui_inbound_id, sub_url)
VALUES (1, 'default', 1, 'https://fi.kereal.qzz.io/pnl', 'xxx-yyy-zzz', 2, 'https://sub.kereal.qzz.io/s/');

INSERT OR IGNORE INTO plans (id, name, price, devices_limit, traffic_limit, duration)
VALUES (1, 'trial', 0, 1, 1073741824, 3),
       (2, 'free', 0, 1, 10737418240, 0);

INSERT OR IGNORE INTO plan_sources (plan_id, source_id) VALUES (1, 1), (2, 1);

UPDATE subscriptions SET plan_id = 2 WHERE plan_id IS NULL;
