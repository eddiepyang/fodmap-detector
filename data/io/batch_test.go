package io

import (
	"bufio"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"fodmap/data/schemas"
)

// inlineParser is a parseSchemaFunc that uses plain JSON unmarshaling.
func inlineParser(_ *regexp.Regexp, b []byte) (schemas.Review, error) {
	var r schemas.Review
	return r, json.Unmarshal(b, &r)
}

// errorParser returns an error for any line containing "BAD", otherwise parses normally.
func errorParser(_ *regexp.Regexp, b []byte) (schemas.Review, error) {
	if strings.Contains(string(b), "BAD") {
		return schemas.Review{}, fmt.Errorf("intentional parse error")
	}
	var r schemas.Review
	return r, json.Unmarshal(b, &r)
}

func makeScanner(s string) *bufio.Scanner {
	return bufio.NewScanner(strings.NewReader(s))
}

var threeReviews = strings.Join([]string{
	`{"review_id":"r1","user_id":"u1","business_id":"b1","stars":5.0,"useful":0,"funny":0,"cool":0,"text":"A"}`,
	`{"review_id":"r2","user_id":"u2","business_id":"b2","stars":4.0,"useful":1,"funny":0,"cool":0,"text":"B"}`,
	`{"review_id":"r3","user_id":"u3","business_id":"b3","stars":3.0,"useful":0,"funny":1,"cool":0,"text":"C"}`,
}, "\n")

func collect(inChan chan ParseResult) []ParseResult {
	var results []ParseResult
	for item := range inChan {
		results = append(results, item)
	}
	return results
}

// TestReadToChan_AllRows verifies all rows are emitted when stop=0 (no limit).
func TestReadToChan_AllRows(t *testing.T) {
	inChan := make(chan ParseResult, 10)
	go ReadToChan(inlineParser, inChan, makeScanner(threeReviews), 0)
	results := collect(inChan)

	if len(results) != 3 {
		t.Fatalf("got %d rows, want 3", len(results))
	}
	if results[0].Record.ReviewID != "r1" {
		t.Errorf("results[0].Record.ReviewID = %q, want %q", results[0].Record.ReviewID, "r1")
	}
	if results[2].Record.ReviewID != "r3" {
		t.Errorf("results[2].Record.ReviewID = %q, want %q", results[2].Record.ReviewID, "r3")
	}
}

// TestReadToChan_EarlyStop verifies that stop=1 causes the goroutine to
// stop after processing 2 items (counter 0 and 1).
func TestReadToChan_EarlyStop(t *testing.T) {
	inChan := make(chan ParseResult, 10)
	go ReadToChan(inlineParser, inChan, makeScanner(threeReviews), 1)
	results := collect(inChan)

	if len(results) != 2 {
		t.Errorf("got %d rows, want 2 (stop=1 → counter 0 and 1)", len(results))
	}
}

// TestReadToChan_EmptyInput verifies no rows are emitted for an empty scanner.
func TestReadToChan_EmptyInput(t *testing.T) {
	inChan := make(chan ParseResult, 10)
	go ReadToChan(inlineParser, inChan, makeScanner(""), 0)
	results := collect(inChan)

	if len(results) != 0 {
		t.Errorf("got %d rows, want 0 for empty input", len(results))
	}
}

// TestReadToChan_FieldValues verifies parsed field values are correct.
func TestReadToChan_FieldValues(t *testing.T) {
	inChan := make(chan ParseResult, 10)
	input := `{"review_id":"x1","user_id":"u9","business_id":"b9","stars":4.5,"useful":3,"funny":1,"cool":2,"text":"Nice place"}`
	go ReadToChan(inlineParser, inChan, makeScanner(input), 0)
	results := collect(inChan)

	if len(results) != 1 {
		t.Fatalf("got %d rows, want 1", len(results))
	}
	r := results[0].Record
	if r.ReviewID != "x1" {
		t.Errorf("ReviewID = %q, want %q", r.ReviewID, "x1")
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

// TestReadToChan_ParserError verifies that error records are sent through the
// channel with Err set, and that valid records still carry Err=nil.
func TestReadToChan_ParserError(t *testing.T) {
	inChan := make(chan ParseResult, 10)
	input := strings.Join([]string{
		`{"review_id":"good","user_id":"u1","business_id":"b1","stars":5.0,"useful":0,"funny":0,"cool":0,"text":"A"}`,
		`BAD line that will fail parsing`,
	}, "\n")

	go ReadToChan(errorParser, inChan, makeScanner(input), 0)
	results := collect(inChan)

	if len(results) != 2 {
		t.Fatalf("got %d results, want 2 (all lines sent through channel)", len(results))
	}

	var errCount int
	for _, r := range results {
		if r.Err != nil {
			errCount++
		}
	}
	if errCount != 1 {
		t.Errorf("errCount = %d, want 1", errCount)
	}

	var valid ParseResult
	for _, r := range results {
		if r.Err == nil {
			valid = r
		}
	}
	if valid.Record.ReviewID != "good" {
		t.Errorf("valid record ReviewID = %q, want %q", valid.Record.ReviewID, "good")
	}
}
