-- Revert embedding columns back to vector(768) (float32).
-- Drop HNSW indexes first (halfvec_cosine_ops is incompatible with vector).

DROP INDEX IF EXISTS idx_menu_items_embedding;
ALTER TABLE menu_items ALTER COLUMN embedding TYPE vector(768) USING embedding::vector(768);
CREATE INDEX IF NOT EXISTS idx_menu_items_embedding ON menu_items USING hnsw (embedding vector_cosine_ops);

DROP INDEX IF EXISTS idx_review_chunks_embedding;
ALTER TABLE review_chunks ALTER COLUMN embedding TYPE vector(768) USING embedding::vector(768);
CREATE INDEX IF NOT EXISTS idx_review_chunks_embedding ON review_chunks USING hnsw (embedding vector_cosine_ops);

DROP INDEX IF EXISTS idx_fodmap_embedding;
ALTER TABLE fodmap_ingredients ALTER COLUMN embedding TYPE vector(768) USING embedding::vector(768);
CREATE INDEX IF NOT EXISTS idx_fodmap_embedding ON fodmap_ingredients USING hnsw (embedding vector_cosine_ops);
