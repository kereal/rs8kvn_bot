ALTER TABLE nodes RENAME TO sources;

ALTER TABLE sources DROP COLUMN type;

ALTER TABLE sources RENAME COLUMN is_active TO active;
ALTER TABLE sources RENAME COLUMN host TO x_ui_host;
ALTER TABLE sources RENAME COLUMN api_token TO x_ui_api_token;
ALTER TABLE sources RENAME COLUMN inbound_id TO x_ui_inbound_id;
ALTER TABLE sources RENAME COLUMN subscription_url TO sub_url;

ALTER TABLE plan_nodes RENAME TO plan_sources;
ALTER TABLE plan_nodes RENAME COLUMN node_id TO source_id;
