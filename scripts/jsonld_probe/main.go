// Command jsonld_probe measures the Tier-0 JSON-LD hit rate over already-fetched
// bronze HTML. It answers the "should the extraction cascade move to Python?"
// question (docs/plans/anti-scraping-bypass-plan.md): JSON-LD is the only
// extraction tier that runs purely in Go with no LLM call, so its share of pages
// bounds how much unique work the Go cascade carries. A low hit rate means most
// pages already route through the Python LLM/OCR paths, making consolidation cheap.
//
// It runs the EXACT production detector (scraper.ExtractJSONLD) over one HTML file
// per distinct restaurant (stripping the "-<attempt>" suffix from bronze
// filenames) and reports per-page hits plus an overall rate.
//
// Fast, offline, no network/DB. NOT part of `make check` — it's a measurement aid.
//
// Usage:
//
//	go run ./scripts/jsonld_probe [--dir data/bronze/restaurants]
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"fodmap/scraper"
)

func main() {
	dir := flag.String("dir", "data/bronze/restaurants", "root directory of bronze restaurant HTML")
	flag.Parse()

	// One file per distinct restaurant id (filename without the "-<attempt>" suffix).
	files := map[string]string{}
	err := filepath.Walk(*dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".html") {
			return nil
		}
		base := strings.TrimSuffix(filepath.Base(p), ".html")
		id := base
		if i := strings.LastIndex(base, "-"); i > 0 {
			id = base[:i]
		}
		if _, ok := files[id]; !ok {
			files[id] = p
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "walk:", err)
		os.Exit(1)
	}
	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "no .html files under %s\n", *dir)
		os.Exit(1)
	}

	ids := make([]string, 0, len(files))
	for id := range files {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	hits := 0
	for _, id := range ids {
		b, readErr := os.ReadFile(files[id])
		if readErr != nil {
			fmt.Printf("%-12s ERR  %v\n", id, readErr)
			continue
		}
		items, meta, ok := scraper.ExtractJSONLD(strings.NewReader(string(b)))
		mark := "miss"
		if ok {
			hits++
			mark = "HIT"
		}
		fmt.Printf("%-12s %-4s items=%-4d name=%q\n", id, mark, len(items), meta.RestaurantName)
	}
	fmt.Printf("\nTier-0 JSON-LD hit rate: %d/%d distinct pages (%.0f%%)\n",
		hits, len(ids), 100*float64(hits)/float64(len(ids)))
}
