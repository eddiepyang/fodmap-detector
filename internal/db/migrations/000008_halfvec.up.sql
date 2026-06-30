-- Upgrade pgvector extension to latest (0.8.x) for HNSW vacuum fix + insert perf.
-- The extension version is whatever the DB has installed; ALTER EXTENSION
-- UPDATE pulls the latest available from the OS package.
ALTER EXTENSION vector UPDATE;

-- Convert embedding columns from vector(768) to halfvec(768) across all
-- tables. halfvec stores float16 (2 bytes/dim) instead of float32 (4 bytes/dim),
-- halving storage and ~2x-ing HNSW scan speed for the same recall. The cosine
-- distance operator (<=>) works identically on halfvec.
--
-- The HNSW index must be dropped BEFORE the column type change (the old
-- vector_cosine_ops opclass is incompatible with halfvec) and recreated
-- afterwards with halfvec_cosine_ops.

-- 1. menu_items
DROP INDEX IF EXISTS idx_menu_items_embedding;
ALTER TABLE menu_items ALTER COLUMN embedding TYPE halfvec(768) USING embedding::halfvec(768);
CREATE INDEX IF NOT EXISTS idx_menu_items_embedding ON menu_items USING hnsw (embedding halfvec_cosine_ops);

-- 2. review_chunks
DROP INDEX IF EXISTS idx_review_chunks_embedding;
ALTER TABLE review_chunks ALTER COLUMN embedding TYPE halfvec(768) USING embedding::halfvec(768);
CREATE INDEX IF NOT EXISTS idx_review_chunks_embedding ON review_chunks USING hnsw (embedding halfvec_cosine_ops);

-- 3. fodmap_ingredients
DROP INDEX IF EXISTS idx_fodmap_embedding;
ALTER TABLE fodmap_ingredients ALTER COLUMN embedding TYPE halfvec(768) USING embedding::halfvec(768);
CREATE INDEX IF NOT EXISTS idx_fodmap_embedding ON fodmap_ingredients USING hnsw (embedding halfvec_cosine_ops);
