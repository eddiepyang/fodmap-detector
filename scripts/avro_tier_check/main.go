// Command avro_tier_check verifies the menu-extraction Avro contract around the
// extraction_tier field (added for tier-mix telemetry):
//
//  1. Round-trip: a record written with WriteMenuExtractionAvro reads back with
//     extraction_tier intact.
//  2. Backward compat: an existing OCF file written before the field existed
//     still decodes without error (OCF embeds the writer schema per file, so the
//     new reader must not choke on the missing field).
//
// Fast, offline, no network/DB. Exits non-zero on any failure so it can gate a
// release. NOT part of `make check` today, but safe to wire in.
//
// Usage:
//
//	go run ./scripts/avro_tier_check [--old-dir data/silver/menus]
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"fodmap/menusearch"
	"fodmap/search"

	"github.com/hamba/avro/v2/ocf"
)

func decodeFirst(path string) (map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	dec, err := ocf.NewDecoder(f)
	if err != nil {
		return nil, err
	}
	var rec map[string]any
	if dec.HasNext() {
		if err := dec.Decode(&rec); err != nil {
			return nil, err
		}
	}
	return rec, dec.Error()
}

func main() {
	oldDir := flag.String("old-dir", "data/silver/menus", "dir to scan for a pre-existing .avro (backward-compat check)")
	flag.Parse()

	failf := func(format string, a ...any) {
		fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", a...)
		os.Exit(1)
	}

	// 1. Round-trip.
	tmp := filepath.Join(os.TempDir(), "avro-tier-check.avro")
	defer func() { _ = os.Remove(tmp) }()

	want := "html_llm"
	rec := menusearch.MenuExtractionRecord{
		BusinessID:     "TEST123",
		SourceURL:      "https://example.com/menu",
		RestaurantName: "Test Diner",
		Items:          []search.MenuItem{{DishName: "Fries", Description: "salty"}},
		EventID:        "evt", JobID: "1", Attempt: 1, DiscoveryEventID: "disc",
		ExtractionTier: want,
	}
	if err := menusearch.WriteMenuExtractionAvro(context.Background(), tmp, rec); err != nil {
		failf("write: %v", err)
	}
	got, err := decodeFirst(tmp)
	if err != nil {
		failf("read back: %v", err)
	}
	if got["extraction_tier"] != want {
		failf("round-trip extraction_tier = %q, want %q", got["extraction_tier"], want)
	}
	fmt.Printf("ok: round-trip extraction_tier=%q\n", want)

	// 2. Backward compat against the first pre-existing .avro found, if any.
	var oldPath string
	_ = filepath.Walk(*oldDir, func(p string, info os.FileInfo, err error) error {
		if err == nil && oldPath == "" && !info.IsDir() && filepath.Ext(p) == ".avro" && p != tmp {
			oldPath = p
		}
		return nil
	})
	if oldPath == "" {
		fmt.Printf("skip: no pre-existing .avro under %s (backward-compat check)\n", *oldDir)
		return
	}
	oldRec, err := decodeFirst(oldPath)
	if err != nil {
		failf("decode existing %s: %v", oldPath, err)
	}
	_, hasField := oldRec["extraction_tier"]
	fmt.Printf("ok: existing file decodes (%s); camis=%v extraction_tier present=%v\n",
		filepath.Base(oldPath), oldRec["camis"], hasField)
}
