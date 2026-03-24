package data

import (
	"regexp"
	"testing"
)


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


