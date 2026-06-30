-- ═══ restaurants: surrogate UUID PK + external IDs ═══
ALTER TABLE restaurants ADD COLUMN id UUID PRIMARY KEY DEFAULT gen_random_uuid();
ALTER TABLE restaurants ALTER COLUMN camis DROP NOT NULL;
ALTER TABLE restaurants ADD CONSTRAINT restaurants_camis_unique UNIQUE (camis);
ALTER TABLE restaurants ADD COLUMN yelp_id TEXT UNIQUE;

-- ═══ menu_items: FK → UUID, TRUNCATE, rename scraped_at, add timestamps ═══
ALTER TABLE menu_items DROP CONSTRAINT IF EXISTS menu_items_business_id_fkey;
TRUNCATE TABLE menu_items;
ALTER TABLE menu_items DROP COLUMN business_id;
ALTER TABLE menu_items ADD COLUMN business_id UUID NOT NULL;
ALTER TABLE menu_items ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE menu_items ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE menu_items RENAME COLUMN scraped_at_utc TO scraped_at;
ALTER TABLE menu_items ALTER COLUMN scraped_at TYPE TIMESTAMPTZ USING scraped_at::timestamptz;
ALTER TABLE menu_items
    ADD CONSTRAINT menu_items_business_id_fkey
    FOREIGN KEY (business_id) REFERENCES restaurants(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS idx_menu_items_business_id ON menu_items(business_id);

-- ═══ reviews: FK → UUID, TRUNCATE, add created_at, add index ═══
TRUNCATE TABLE reviews CASCADE;
ALTER TABLE reviews DROP COLUMN business_id;
ALTER TABLE reviews ADD COLUMN business_id UUID REFERENCES restaurants(id) ON DELETE CASCADE;
ALTER TABLE reviews ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
CREATE INDEX IF NOT EXISTS idx_reviews_business_id ON reviews(business_id);

-- ═══ conversations: FK → UUID, TRUNCATE, convert timestamps ═══
TRUNCATE TABLE conversations CASCADE;
ALTER TABLE conversations DROP COLUMN business_id;
ALTER TABLE conversations ADD COLUMN business_id UUID NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE;
ALTER TABLE conversations ALTER COLUMN created_at TYPE TIMESTAMPTZ USING created_at AT TIME ZONE 'UTC';
ALTER TABLE conversations ALTER COLUMN updated_at TYPE TIMESTAMPTZ USING updated_at AT TIME ZONE 'UTC';

-- ═══ users: add updated_at, convert created_at ═══
ALTER TABLE users ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE users ALTER COLUMN created_at TYPE TIMESTAMPTZ USING created_at AT TIME ZONE 'UTC';

-- ═══ user_profiles: convert timestamps ═══
ALTER TABLE user_profiles ALTER COLUMN created_at TYPE TIMESTAMPTZ USING created_at AT TIME ZONE 'UTC';
ALTER TABLE user_profiles ALTER COLUMN updated_at TYPE TIMESTAMPTZ USING updated_at AT TIME ZONE 'UTC';

-- ═══ messages: convert created_at ═══
ALTER TABLE messages ALTER COLUMN created_at TYPE TIMESTAMPTZ USING created_at AT TIME ZONE 'UTC';

-- ═══ extraction_rules: add updated_at ═══
ALTER TABLE extraction_rules ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- ═══ fodmap_catalog: add created_at, convert updated_at ═══
ALTER TABLE fodmap_catalog ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE fodmap_catalog ALTER COLUMN updated_at TYPE TIMESTAMPTZ USING updated_at AT TIME ZONE 'UTC';

-- ═══ review_chunks: add created_at ═══
ALTER TABLE review_chunks ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- ═══ fodmap_ingredients: add created_at ═══
ALTER TABLE fodmap_ingredients ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- ═══ Trigger function (shared) ═══
CREATE OR REPLACE FUNCTION touch_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- ═══ Per-table BEFORE UPDATE triggers on mutable tables ═══
CREATE TRIGGER trg_users_updated_at             BEFORE UPDATE ON users           FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
CREATE TRIGGER trg_user_profiles_updated_at   BEFORE UPDATE ON user_profiles  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
CREATE TRIGGER trg_conversations_updated_at   BEFORE UPDATE ON conversations  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
CREATE TRIGGER trg_restaurants_updated_at     BEFORE UPDATE ON restaurants    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
CREATE TRIGGER trg_sources_updated_at         BEFORE UPDATE ON sources        FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
CREATE TRIGGER trg_extraction_rules_updated_at BEFORE UPDATE ON extraction_rules FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
CREATE TRIGGER trg_menu_items_updated_at      BEFORE UPDATE ON menu_items     FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
CREATE TRIGGER trg_fodmap_catalog_updated_at  BEFORE UPDATE ON fodmap_catalog  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();