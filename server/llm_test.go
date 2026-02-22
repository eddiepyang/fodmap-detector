package server

import (
	"fmt"
	"testing"

	"fodmap/data/schemas"
)

func makeReviews(n int) []schemas.ReviewSchemaS {
	out := make([]schemas.ReviewSchemaS, n)
	for i := range out {
		out[i] = schemas.ReviewSchemaS{ReviewId: fmt.Sprintf("r%d", i)}
	}
	return out
}

func TestChunkReviews_ExactMultiple(t *testing.T) {
	chunks := chunkReviews(makeReviews(10), 5)
	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2", len(chunks))
	}
	if len(chunks[0]) != 5 {
		t.Errorf("chunks[0] len = %d, want 5", len(chunks[0]))
	}
	if len(chunks[1]) != 5 {
		t.Errorf("chunks[1] len = %d, want 5", len(chunks[1]))
	}
}

func TestChunkReviews_Remainder(t *testing.T) {
	chunks := chunkReviews(makeReviews(11), 5)
	if len(chunks) != 3 {
		t.Fatalf("got %d chunks, want 3", len(chunks))
	}
	if len(chunks[2]) != 1 {
		t.Errorf("last chunk len = %d, want 1", len(chunks[2]))
	}
}

func TestChunkReviews_SmallerThanChunk(t *testing.T) {
	chunks := chunkReviews(makeReviews(3), 5)
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	if len(chunks[0]) != 3 {
		t.Errorf("chunk len = %d, want 3", len(chunks[0]))
	}
}

func TestChunkReviews_Empty(t *testing.T) {
	chunks := chunkReviews(nil, 5)
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	if len(chunks[0]) != 0 {
		t.Errorf("expected empty chunk, got len %d", len(chunks[0]))
	}
}

func TestChunkReviews_SingleItem(t *testing.T) {
	chunks := chunkReviews(makeReviews(1), 5)
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	if len(chunks[0]) != 1 {
		t.Errorf("chunk len = %d, want 1", len(chunks[0]))
	}
}

func TestChunkReviews_PreservesOrder(t *testing.T) {
	reviews := makeReviews(7)
	chunks := chunkReviews(reviews, 3)
	// Expected: [r0,r1,r2], [r3,r4,r5], [r6]
	if len(chunks) != 3 {
		t.Fatalf("got %d chunks, want 3", len(chunks))
	}
	if chunks[0][0].ReviewId != "r0" {
		t.Errorf("chunks[0][0].ReviewId = %q, want r0", chunks[0][0].ReviewId)
	}
	if chunks[1][0].ReviewId != "r3" {
		t.Errorf("chunks[1][0].ReviewId = %q, want r3", chunks[1][0].ReviewId)
	}
	if chunks[2][0].ReviewId != "r6" {
		t.Errorf("chunks[2][0].ReviewId = %q, want r6", chunks[2][0].ReviewId)
	}
}
