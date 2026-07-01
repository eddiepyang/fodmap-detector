-- Reverse all changes from 000010_schema_consistency.up.sql.

-- Drop triggers
DROP TRIGGER IF EXISTS trg_fodmap_catalog_updated_at ON fodmap_catalog;
DROP TRIGGER IF EXISTS trg_menu_items_updated_at ON menu_items;
DROP TRIGGER IF EXISTS trg_extraction_rules_updated_at ON extraction_rules;
DROP TRIGGER IF EXISTS trg_sources_updated_at ON sources;
DROP TRIGGER IF EXISTS trg_restaurants_updated_at ON restaurants;
DROP TRIGGER IF EXISTS trg_conversations_updated_at ON conversations;
DROP TRIGGER IF EXISTS trg_user_profiles_updated_at ON user_profiles;
DROP TRIGGER IF EXISTS trg_users_updated_at ON users;

-- Drop trigger function
DROP FUNCTION IF EXISTS touch_updated_at();

-- Revert fodmap_ingredients: drop created_at
ALTER TABLE fodmap_ingredients DROP COLUMN IF EXISTS created_at;

-- Revert review_chunks: drop created_at
ALTER TABLE review_chunks DROP COLUMN IF EXISTS created_at;

-- Revert fodmap_catalog: drop created_at, revert updated_at to TIMESTAMP
ALTER TABLE fodmap_catalog DROP COLUMN IF EXISTS created_at;
ALTER TABLE fodmap_catalog ALTER COLUMN updated_at TYPE TIMESTAMP USING updated_at AT TIME ZONE 'UTC';

-- Revert extraction_rules: drop updated_at
ALTER TABLE extraction_rules DROP COLUMN IF EXISTS updated_at;

-- Revert messages: revert created_at to TIMESTAMP
ALTER TABLE messages ALTER COLUMN created_at TYPE TIMESTAMP USING created_at AT TIME ZONE 'UTC';

-- Revert user_profiles: revert timestamps to TIMESTAMP
ALTER TABLE user_profiles ALTER COLUMN created_at TYPE TIMESTAMP USING created_at AT TIME ZONE 'UTC';
ALTER TABLE user_profiles ALTER COLUMN updated_at TYPE TIMESTAMP USING updated_at AT TIME ZONE 'UTC';

-- Revert users: drop updated_at, revert created_at to TIMESTAMP
ALTER TABLE users DROP COLUMN IF EXISTS updated_at;
ALTER TABLE users ALTER COLUMN created_at TYPE TIMESTAMP USING created_at AT TIME ZONE 'UTC';

-- Revert conversations: revert timestamps to TIMESTAMP, restore business_id as TEXT NOT NULL
ALTER TABLE conversations ALTER COLUMN created_at TYPE TIMESTAMP USING created_at AT TIME ZONE 'UTC';
ALTER TABLE conversations ALTER COLUMN updated_at TYPE TIMESTAMP USING updated_at AT TIME ZONE 'UTC';
ALTER TABLE conversations DROP COLUMN IF EXISTS business_id;
ALTER TABLE conversations ADD COLUMN business_id TEXT NOT NULL DEFAULT '';

-- Revert reviews: drop created_at, drop index, restore business_id as TEXT
DROP INDEX IF EXISTS idx_reviews_business_id;
ALTER TABLE reviews DROP COLUMN IF EXISTS created_at;
ALTER TABLE reviews DROP COLUMN IF EXISTS business_id;
ALTER TABLE reviews ADD COLUMN business_id TEXT;

-- Revert menu_items: restore business_id as TEXT, rename scraped_at back, drop timestamps
ALTER TABLE menu_items DROP CONSTRAINT IF EXISTS menu_items_business_id_fkey;
DROP INDEX IF EXISTS idx_menu_items_business_id;
ALTER TABLE menu_items DROP COLUMN IF EXISTS created_at;
ALTER TABLE menu_items DROP COLUMN IF EXISTS updated_at;
ALTER TABLE menu_items ALTER COLUMN scraped_at TYPE TEXT USING scraped_at::text;
ALTER TABLE menu_items RENAME COLUMN scraped_at TO scraped_at_utc;
ALTER TABLE menu_items DROP COLUMN IF EXISTS business_id;
ALTER TABLE menu_items ADD COLUMN business_id TEXT NOT NULL DEFAULT '';
ALTER TABLE menu_items ADD CONSTRAINT menu_items_business_id_fkey
    FOREIGN KEY (business_id) REFERENCES restaurants(camis) ON DELETE CASCADE;

-- Revert restaurants: drop yelp_id, restore camis as NOT NULL PK, drop surrogate id.
-- Order matters: menu_items_business_id_fkey (re-created above) references
-- restaurants(camis) via restaurants_camis_unique, so we must NOT drop that
-- unique constraint directly. Instead, drop the surrogate PK on id, then ADD
-- PRIMARY KEY (camis) — PostgreSQL converts the existing restaurants_camis_unique
-- constraint into the PK in place, preserving the FK dependency. Only then is
-- it safe to DROP COLUMN id.
ALTER TABLE restaurants DROP COLUMN IF EXISTS yelp_id;
ALTER TABLE restaurants DROP CONSTRAINT IF EXISTS restaurants_pkey;
ALTER TABLE restaurants ALTER COLUMN camis SET NOT NULL;
ALTER TABLE restaurants ADD PRIMARY KEY (camis);
ALTER TABLE restaurants DROP COLUMN IF EXISTS id;