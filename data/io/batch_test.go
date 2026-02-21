package io

import (
	"bufio"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"fodmap/data/schemas"
)

// inlineParser is a parseSchemaFunc that uses plain JSON unmarshaling.
func inlineParser(_ *regexp.Regexp, b []byte) schemas.ReviewSchemaS {
	var r schemas.ReviewSchemaS
	if err := json.Unmarshal(b, &r); err != nil {
		panic(err)
	}
	return r
}

func makeScanner(s string) *bufio.Scanner {
	return bufio.NewScanner(strings.NewReader(s))
}

var threeReviews = strings.Join([]string{
	`{"review_id":"r1","user_id":"u1","business_id":"b1","stars":5.0,"useful":0,"funny":0,"cool":0,"text":"A"}`,
	`{"review_id":"r2","user_id":"u2","business_id":"b2","stars":4.0,"useful":1,"funny":0,"cool":0,"text":"B"}`,
	`{"review_id":"r3","user_id":"u3","business_id":"b3","stars":3.0,"useful":0,"funny":1,"cool":0,"text":"C"}`,
}, "\n")

// collectFromChannels drains inChan until doneCh fires, then drains any
// remaining buffered items once the channel is closed.
func collectFromChannels(inChan chan schemas.ReviewSchemaS, doneCh chan struct{}) []schemas.ReviewSchemaS {
	var results []schemas.ReviewSchemaS
	for {
		select {
		case item, ok := <-inChan:
			if ok {
				results = append(results, item)
			}
		case <-doneCh:
			for item := range inChan {
				results = append(results, item)
			}
			return results
		}
	}
}

// TestReadToChan_AllRows verifies all rows are emitted when stop=0 (no limit).
func TestReadToChan_AllRows(t *testing.T) {
	inChan := make(chan schemas.ReviewSchemaS, 10)
	doneCh := make(chan struct{})

	go ReadToChan(inlineParser, inChan, doneCh, makeScanner(threeReviews), 0)

	results := collectFromChannels(inChan, doneCh)

	if len(results) != 3 {
		t.Fatalf("got %d rows, want 3", len(results))
	}
	if results[0].ReviewId != "r1" {
		t.Errorf("results[0].ReviewId = %q, want %q", results[0].ReviewId, "r1")
	}
	if results[2].ReviewId != "r3" {
		t.Errorf("results[2].ReviewId = %q, want %q", results[2].ReviewId, "r3")
	}
}

// TestReadToChan_EarlyStop verifies that stop=1 causes the goroutine to
// signal done after processing 2 items (counter 0 and 1).
func TestReadToChan_EarlyStop(t *testing.T) {
	inChan := make(chan schemas.ReviewSchemaS, 10)
	doneCh := make(chan struct{})

	go ReadToChan(inlineParser, inChan, doneCh, makeScanner(threeReviews), 1)

	results := collectFromChannels(inChan, doneCh)

	if len(results) != 2 {
		t.Errorf("got %d rows, want 2 (stop=1 → counter 0 and 1)", len(results))
	}
}

// TestReadToChan_EmptyInput verifies no rows and an immediate done signal
// for an empty scanner.
func TestReadToChan_EmptyInput(t *testing.T) {
	inChan := make(chan schemas.ReviewSchemaS, 10)
	doneCh := make(chan struct{})

	go ReadToChan(inlineParser, inChan, doneCh, makeScanner(""), 0)

	results := collectFromChannels(inChan, doneCh)

	if len(results) != 0 {
		t.Errorf("got %d rows, want 0 for empty input", len(results))
	}
}

// TestReadToChan_FieldValues verifies parsed field values are correct.
func TestReadToChan_FieldValues(t *testing.T) {
	inChan := make(chan schemas.ReviewSchemaS, 10)
	doneCh := make(chan struct{})

	input := `{"review_id":"x1","user_id":"u9","business_id":"b9","stars":4.5,"useful":3,"funny":1,"cool":2,"text":"Nice place"}`
	go ReadToChan(inlineParser, inChan, doneCh, makeScanner(input), 0)

	results := collectFromChannels(inChan, doneCh)

	if len(results) != 1 {
		t.Fatalf("got %d rows, want 1", len(results))
	}
	r := results[0]
	if r.ReviewId != "x1" {
		t.Errorf("ReviewId = %q, want %q", r.ReviewId, "x1")
	}
	if r.Stars != 4.5 {
		t.Errorf("Stars = %v, want 4.5", r.Stars)
	}
	if r.Useful != 3 {
		t.Errorf("Useful = %v, want 3", r.Useful)
	}
	if r.Text != "Nice place" {
		t.Errorf("Text = %q, want %q", r.Text, "Nice place")
	}
}
