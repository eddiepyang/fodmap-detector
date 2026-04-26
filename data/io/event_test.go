package io

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"fodmap/data/schemas"
)

const outfile = "outfile.avro"

// setUp ensures the outfile artifact is populated for TestReadFile.
// It regenerates the file if it is missing or empty.
func setUp() {
	info, err := os.Stat(outfile)
	if errors.Is(err, os.ErrNotExist) || (err == nil && info.Size() == 0) {
		writeRecords(outfile, avroSampleJSONL)
	}
}

// writeRecords is a test helper that writes JSONL lines to an Avro file via EventWriter.
func writeRecords(path, jsonl string) {
	f, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	w, err := NewEventWriter(f, schemas.EventSchema)
	if err != nil {
		panic(err)
	}
	defer func() { _ = w.Close() }()

	for _, line := range strings.Split(jsonl, "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			panic(err)
		}
		if err := w.Write(record); err != nil {
			panic(err)
		}
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
	writeRecords(path, avroSampleJSONL)

	if err := ReadFile(path); err != nil {
		t.Errorf("ReadFile after EventWriter: %v", err)
	}
}

// TestReadFile_MissingFile verifies an error is returned for a missing file.
func TestReadFile_MissingFile(t *testing.T) {
	err := ReadFile("/does/not/exist.avro")
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}
