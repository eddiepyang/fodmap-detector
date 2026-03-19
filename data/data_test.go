package data

import (
	"bufio"
	"regexp"
	"strings"
	"testing"

	"fodmap/data/schemas"
)

var testReviewsJSONL = strings.Join([]string{
	`{"review_id":"r1","user_id":"u1","business_id":"b1","stars":4.5,"useful":2,"funny":1,"cool":3,"text":"Great food here!"}`,
	`{"review_id":"r2","user_id":"u2","business_id":"b2","stars":2.0,"useful":0,"funny":0,"cool":0,"text":"Disappointing."}`,
}, "\n")

func TestUnmarshalReview(t *testing.T) {
	pattern := regexp.MustCompile(`[a-zA-Z0-9'-]+`)

	tests := []struct {
		name       string
		input      string
		wantErr    bool
		wantID     string
		wantUserID string
		wantStars  float32
		wantUseful int32
		wantCool   int32
		wantText   string
	}{
		{
			name:       "full valid review",
			input:      `{"review_id":"abc123","user_id":"u1","business_id":"b1","stars":4.5,"useful":2,"funny":1,"cool":3,"text":"Great food!"}`,
			wantID:     "abc123",
			wantUserID: "u1",
			wantStars:  4.5,
			wantUseful: 2,
			wantCool:   3,
			wantText:   "Great food!",
		},
		{
			name:       "zero numeric fields",
			input:      `{"review_id":"zero","user_id":"u2","business_id":"b2","stars":1.0,"useful":0,"funny":0,"cool":0,"text":"Bad."}`,
			wantID:     "zero",
			wantUserID: "u2",
			wantStars:  1.0,
			wantUseful: 0,
			wantCool:   0,
			wantText:   "Bad.",
		},
		{
			name:       "unknown fields are ignored",
			input:      `{"review_id":"r3","user_id":"u3","business_id":"b3","stars":5.0,"useful":0,"funny":0,"cool":0,"text":"Wow","extra":"ignored"}`,
			wantID:     "r3",
			wantUserID: "u3",
			wantStars:  5.0,
			wantUseful: 0,
			wantCool:   0,
			wantText:   "Wow",
		},
		{
			name:    "invalid JSON returns error",
			input:   `{not valid json`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := UnmarshalReview(pattern, []byte(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.ReviewID != tt.wantID {
				t.Errorf("ReviewID = %q, want %q", got.ReviewID, tt.wantID)
			}
			if got.UserID != tt.wantUserID {
				t.Errorf("UserID = %q, want %q", got.UserID, tt.wantUserID)
			}
			if got.Stars != tt.wantStars {
				t.Errorf("Stars = %v, want %v", got.Stars, tt.wantStars)
			}
			if got.Useful != tt.wantUseful {
				t.Errorf("Useful = %v, want %v", got.Useful, tt.wantUseful)
			}
			if got.Cool != tt.wantCool {
				t.Errorf("Cool = %v, want %v", got.Cool, tt.wantCool)
			}
			if got.Text != tt.wantText {
				t.Errorf("Text = %q, want %q", got.Text, tt.wantText)
			}
		})
	}
}

// TestWriteAndReadParquet is an integration test that verifies the full
// write→read roundtrip using in-memory JSONL data and a temp file.
func TestWriteAndReadParquet(t *testing.T) {
	path := t.TempDir() + "/test.parquet"

	scanner := bufio.NewScanner(strings.NewReader(testReviewsJSONL))
	if err := WriteBatchParquet(path, scanner, 0); err != nil {
		t.Fatalf("WriteBatchParquet: %v", err)
	}

	result, err := ReadParquet(path, 10)
	if err != nil {
		t.Fatalf("ReadParquet: %v", err)
	}

	rows, ok := result.([]schemas.Review)
	if !ok {
		t.Fatalf("unexpected result type: %T", result)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}

	if rows[0].ReviewID != "r1" {
		t.Errorf("rows[0].ReviewID = %q, want %q", rows[0].ReviewID, "r1")
	}
	if rows[0].Stars != 4.5 {
		t.Errorf("rows[0].Stars = %v, want 4.5", rows[0].Stars)
	}
	if rows[0].Useful != 2 {
		t.Errorf("rows[0].Useful = %v, want 2", rows[0].Useful)
	}
	if rows[1].ReviewID != "r2" {
		t.Errorf("rows[1].ReviewID = %q, want %q", rows[1].ReviewID, "r2")
	}
	if rows[1].Stars != 2.0 {
		t.Errorf("rows[1].Stars = %v, want 2.0", rows[1].Stars)
	}
}

// TestReadParquet_EarlyStop verifies that earlyStop limits rows returned.
func TestReadParquet_EarlyStop(t *testing.T) {
	path := t.TempDir() + "/test.parquet"

	scanner := bufio.NewScanner(strings.NewReader(testReviewsJSONL))
	if err := WriteBatchParquet(path, scanner, 0); err != nil {
		t.Fatalf("WriteBatchParquet: %v", err)
	}

	result, err := ReadParquet(path, 1)
	if err != nil {
		t.Fatalf("ReadParquet: %v", err)
	}

	rows, ok := result.([]schemas.Review)
	if !ok {
		t.Fatalf("unexpected result type: %T", result)
	}
	if len(rows) != 1 {
		t.Errorf("got %d rows, want 1 (earlyStop=1)", len(rows))
	}
}

// TestReadParquet_MissingFile verifies error is returned for a missing file.
func TestReadParquet_MissingFile(t *testing.T) {
	_, err := ReadParquet("/does/not/exist.parquet", 5)
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}
