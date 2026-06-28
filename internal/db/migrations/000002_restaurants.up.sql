CREATE TABLE IF NOT EXISTS restaurants (
    camis           TEXT PRIMARY KEY,          -- NYC DOHMH unique ID
    dba             TEXT NOT NULL,              -- doing-business-as name
    boro            TEXT,
    building        TEXT,
    street          TEXT,
    zipcode         TEXT,
    phone           TEXT,
    cuisine         TEXT,
    latitude        DOUBLE PRECISION,
    longitude       DOUBLE PRECISION,
    nta             TEXT,
    -- Discovery + scrape lifecycle
    status          TEXT NOT NULL DEFAULT 'pending_discovery',
    menu_url        TEXT,
    menu_url_source TEXT,                       -- 'gemini' | 'manual'
    item_count      INTEGER DEFAULT 0,
    scraped_at      TIMESTAMPTZ,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_restaurants_status ON restaurants(status);
CREATE INDEX IF NOT EXISTS idx_restaurants_dba ON restaurants USING gin (to_tsvector('english', dba));
CREATE INDEX IF NOT EXISTS idx_restaurants_nta ON restaurants(nta);
