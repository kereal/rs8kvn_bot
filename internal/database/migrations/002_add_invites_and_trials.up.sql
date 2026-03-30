-- Create invites and trial_requests tables for referral system

CREATE TABLE IF NOT EXISTS invites (
    code VARCHAR(16) PRIMARY KEY,
    referrer_tg_id BIGINT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_invites_referrer ON invites(referrer_tg_id);

CREATE TABLE IF NOT EXISTS trial_requests (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ip VARCHAR(45) NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_trial_requests_ip ON trial_requests(ip);
