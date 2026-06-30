-- Link menu_items.business_id to restaurants.camis via foreign key.
-- Previously menu_items.business_id was a URL-hostname-derived UUID unrelated
-- to restaurants.camis, so the two tables could not be joined. Going forward
-- the scrape pipeline writes the restaurant's camis as business_id, making
-- menu_items.business_id -> restaurants.camis a real relationship.
--
-- Existing rows hold the old URL-derived UUIDs and cannot satisfy the FK, so
-- truncate the table before adding the constraint. Menu items are regenerable
-- by re-scraping, and re-scrapes will populate business_id with the camis.

TRUNCATE TABLE menu_items;

ALTER TABLE menu_items
    ADD CONSTRAINT menu_items_business_id_fkey
    FOREIGN KEY (business_id) REFERENCES restaurants(camis) ON DELETE CASCADE;