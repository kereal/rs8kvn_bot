ALTER TABLE sources RENAME TO nodes;

ALTER TABLE nodes ADD COLUMN type VARCHAR(10) NOT NULL DEFAULT '3x-ui';

ALTER TABLE nodes RENAME COLUMN active TO is_active;
ALTER TABLE nodes RENAME COLUMN x_ui_host TO host;
ALTER TABLE nodes RENAME COLUMN x_ui_api_token TO api_token;
ALTER TABLE nodes RENAME COLUMN x_ui_inbound_id TO inbound_id;
ALTER TABLE nodes RENAME COLUMN sub_url TO subscription_url;

ALTER TABLE plan_sources RENAME TO plan_nodes;
ALTER TABLE plan_nodes RENAME COLUMN source_id TO node_id;
