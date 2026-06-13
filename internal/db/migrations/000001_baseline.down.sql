-- Reverse the baseline: drop all domain tables in reverse dependency order.
-- River's own tables (river_job, etc.) are NOT managed here.

DROP TABLE IF EXISTS menutracking_dead_letter;
DROP TABLE IF EXISTS regulatory_updates;
DROP TABLE IF EXISTS extraction_rules;
DROP TABLE IF EXISTS sources;
DROP TABLE IF EXISTS fodmap_meta;
DROP TABLE IF EXISTS fodmap_catalog;
DROP TABLE IF EXISTS fodmap_ingredients;
DROP TABLE IF EXISTS review_chunks;
DROP TABLE IF EXISTS reviews;
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS conversations;
DROP TABLE IF EXISTS user_profiles;
DROP TABLE IF EXISTS users;