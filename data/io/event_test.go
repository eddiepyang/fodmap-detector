package io

import (
	"bufio"
	"errors"
	"os"
	"strings"
	"testing"

	"fodmap/data/schemas"
)

const outfile = "outfile.avro"

// setUp ensures the outfile artifact exists for TestReadFile.
func setUp() {
	_, err := os.OpenFile(outfile, os.O_RDWR|os.O_CREATE, 0644)
	if errors.Is(err, os.ErrNotExist) {
		// outfile.avro is committed as a test fixture; regenerate if missing.
		scanner := bufio.NewScanner(strings.NewReader(avroSampleJSONL))
		WriteEventFile(scanner, outfile, schemas.EventSchema)
	}
}

// avroSampleJSONL is a small set of Yelp-shaped records matching EventSchema.
// Note: useful/funny/cool must be floats to match the Avro schema (type=float).
var avroSampleJSONL = strings.Join([]string{
	`{"review_id":"r1","user_id":"u1","business_id":"b1","stars":4.5,"useful":1.0,"funny":0.0,"cool":2.0,"text":"Great!"}`,
	`{"review_id":"r2","user_id":"u2","business_id":"b2","stars":3.0,"useful":0.0,"funny":1.0,"cool":0.0,"text":"Ok."}`,
}, "\n")

// TestReadFile tests reading the committed outfile.avro fixture.
func TestReadFile(t *testing.T) {
	setUp()
	if err := ReadFile(outfile); err != nil {
		t.Fatalf("ReadFile(%q): %v", outfile, err)
	}
}

// TestWriteAndReadAvro is a self-contained roundtrip integration test.
// It writes sample records to a temp Avro file and reads them back.
func TestWriteAndReadAvro(t *testing.T) {
	path := t.TempDir() + "/test.avro"

	scanner := bufio.NewScanner(strings.NewReader(avroSampleJSONL))
	WriteEventFile(scanner, path, schemas.EventSchema)

	if err := ReadFile(path); err != nil {
		t.Errorf("ReadFile after WriteEventFile: %v", err)
	}
}

// TestReadFile_MissingFile verifies an error is returned for a missing file.
func TestReadFile_MissingFile(t *testing.T) {
	err := ReadFile("/does/not/exist.avro")
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}
