package search

import (
	"strings"
	"testing"
)

func TestChunkText(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		maxChars int
		overlap  int
		want     []string
	}{
		{
			name:     "shorter than max",
			text:     "hello world",
			maxChars: 20,
			overlap:  0,
			want:     []string{"hello world"},
		},
		{
			name:     "exact max",
			text:     "hello",
			maxChars: 5,
			overlap:  0,
			want:     []string{"hello"},
		},
		{
			name:     "splits on paragraph boundary",
			text:     "First paragraph.\n\nSecond paragraph.",
			maxChars: 20,
			overlap:  0,
			want:     []string{"First paragraph.", "Second paragraph."},
		},
		{
			name:     "splits on sentence boundary",
			text:     "First sentence. Second sentence. Third sentence.",
			maxChars: 25,
			overlap:  0,
			want:     []string{"First sentence.", "Second sentence.", "Third sentence."},
		},
		{
			name:     "splits on word boundary",
			text:     "one two three four five six seven eight",
			maxChars: 15,
			overlap:  0,
			want:     []string{"one two three", "four five six", "seven eight"},
		},
		{
			name:     "last resort character split",
			text:     "abcdefghij",
			maxChars: 5,
			overlap:  0,
			want:     []string{"abcde", "fghij"},
		},
		{
			name:     "negative max chars returns whole text",
			text:     "hello",
			maxChars: -1,
			overlap:  0,
			want:     []string{"hello"},
		},
		{
			name:     "multi-byte runes character split",
			text:     "你好世界这是测试文本",
			maxChars: 5,
			overlap:  0,
			want:     []string{"你好世界这", "是测试文本"},
		},
		{
			name:     "merges small pieces back together",
			text:     "a. b. c. d. e.",
			maxChars: 10,
			overlap:  0,
			want:     []string{"a. b. c.", "d. e."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ChunkText(tt.text, tt.maxChars, tt.overlap)
			if len(got) != len(tt.want) {
				t.Fatalf("ChunkText() returned %d chunks, want %d\ngot:  %v\nwant: %v", len(got), len(tt.want), got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("chunk[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestChunkText_AllChunksUnderMax(t *testing.T) {
	// Property test: no chunk should ever exceed maxChars runes.
	text := "The garlic bread was amazing but the pasta was too salty. I would recommend the risotto instead. Overall a solid 4 stars.\n\nThe gluten-free options were limited but they were very accommodating to my FODMAP diet. The staff knew what FODMAP meant which was refreshing."
	maxChars := 80
	chunks := ChunkText(text, maxChars, 0)

	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}
	for i, chunk := range chunks {
		if len([]rune(chunk)) > maxChars {
			t.Errorf("chunk[%d] has %d runes, exceeds max %d: %q", i, len([]rune(chunk)), maxChars, chunk)
		}
		if chunk == "" {
			t.Errorf("chunk[%d] is empty", i)
		}
	}

	// All original text should be recoverable (minus separator duplication from overlap).
	joined := strings.Join(chunks, "")
	// The joined text won't exactly equal the original because separators are consumed,
	// but all words should be present.
	for _, word := range strings.Fields(text) {
		if !strings.Contains(joined, word) {
			t.Errorf("word %q missing from chunks", word)
		}
	}
}

func TestChunkText_WithOverlap(t *testing.T) {
	text := "First paragraph content here.\n\nSecond paragraph content here."
	maxChars := 30
	chunks := ChunkText(text, maxChars, 5)

	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d: %v", len(chunks), chunks)
	}

	// The second chunk should start with text from the end of the first chunk.
	firstRunes := []rune(chunks[0])
	suffix := string(firstRunes[len(firstRunes)-5:])
	if !strings.HasPrefix(chunks[1], suffix) {
		t.Errorf("chunk[1] = %q should start with overlap %q from chunk[0] = %q", chunks[1], suffix, chunks[0])
	}
}

func TestChunkText_SingleChunkReview(t *testing.T) {
	// Short reviews should produce exactly 1 chunk.
	text := "Great food!"
	chunks := ChunkText(text, 500, 100)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk for short review, got %d: %v", len(chunks), chunks)
	}
	if chunks[0] != text {
		t.Errorf("chunk = %q, want %q", chunks[0], text)
	}
}
