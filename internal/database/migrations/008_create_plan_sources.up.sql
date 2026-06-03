CREATE TABLE IF NOT EXISTS plan_sources (
    plan_id INTEGER NOT NULL,
    source_id INTEGER NOT NULL,
    PRIMARY KEY (plan_id, source_id),
    FOREIGN KEY (plan_id) REFERENCES plans(id) ON DELETE CASCADE,
    FOREIGN KEY (source_id) REFERENCES sources(id) ON DELETE CASCADE
);
