CREATE TABLE IF NOT EXISTS menu_items (
    menu_item_id TEXT PRIMARY KEY,
    business_id TEXT NOT NULL,
    menu_section TEXT,
    restaurant_name TEXT,
    city TEXT,
    state TEXT,
    dish_name TEXT NOT NULL,
    description TEXT,
    stated_ingredients TEXT[],
    has_full_ingredients BOOLEAN NOT NULL DEFAULT FALSE,
    source_url TEXT,
    address TEXT,
    phone_number TEXT,
    scraped_at_utc TEXT,
    embedding VECTOR(768)
);

CREATE INDEX IF NOT EXISTS idx_menu_items_embedding ON menu_items USING hnsw (embedding vector_cosine_ops);
CREATE INDEX IF NOT EXISTS idx_menu_items_business_id ON menu_items(business_id);
