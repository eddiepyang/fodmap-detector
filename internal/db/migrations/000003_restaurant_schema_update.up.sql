ALTER TABLE restaurants RENAME COLUMN menu_url TO website_url;
ALTER TABLE restaurants RENAME COLUMN menu_url_source TO url_source;
ALTER TABLE restaurants ADD COLUMN menu_urls TEXT[] NOT NULL DEFAULT '{}';
