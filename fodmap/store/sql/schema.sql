CREATE TABLE IF NOT EXISTS fodmap_catalog (
    ingredient TEXT PRIMARY KEY,
    level TEXT NOT NULL,
    groups TEXT[] NOT NULL DEFAULT '{}',
    notes TEXT NOT NULL DEFAULT '',
    substitutions TEXT[] NOT NULL DEFAULT '{}',
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS fodmap_meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
