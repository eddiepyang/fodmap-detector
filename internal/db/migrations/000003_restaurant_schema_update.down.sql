ALTER TABLE restaurants DROP COLUMN menu_urls;
ALTER TABLE restaurants RENAME COLUMN url_source TO menu_url_source;
ALTER TABLE restaurants RENAME COLUMN website_url TO menu_url;
