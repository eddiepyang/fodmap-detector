CREATE TABLE IF NOT EXISTS restaurant_menu (
    menu_item_id        TEXT PRIMARY KEY,
    business_id         TEXT NOT NULL,
    menu_section        TEXT NOT NULL,
    restaurant_name     TEXT NOT NULL,
    city                TEXT NOT NULL DEFAULT '',
    state               TEXT NOT NULL DEFAULT '',
    dish_name           TEXT NOT NULL,
    description         TEXT NOT NULL DEFAULT '',
    stated_ingredients  TEXT[] NOT NULL DEFAULT '{}',
    has_full_ingredients BOOLEAN NOT NULL DEFAULT FALSE,
    source_url          TEXT NOT NULL,
    scraped_at          TEXT NOT NULL,
    embedding           vector(768),
    payload             JSONB
);

CREATE INDEX IF NOT EXISTS idx_restaurant_menu_embedding
    ON restaurant_menu USING hnsw (embedding vector_cosine_ops);

CREATE INDEX IF NOT EXISTS idx_restaurant_menu_business_id
    ON restaurant_menu (business_id);
