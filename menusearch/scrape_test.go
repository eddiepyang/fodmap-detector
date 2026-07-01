package menusearch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hamba/avro/v2/ocf"

	"fodmap/search"
)

// assertAttempt checks that the Avro-decoded attempt value equals want.
// hamba/avro decodes Avro int fields as int (not int32) when the target is map[string]any.
func assertAttempt(t *testing.T, got any, want int) bool {
	t.Helper()
	switch v := got.(type) {
	case int:
		if v != want {
			t.Errorf("attempt = %d, want %d", v, want)
			return false
		}
	case int32:
		if int(v) != want {
			t.Errorf("attempt = %d, want %d", v, want)
			return false
		}
	default:
		t.Errorf("attempt type = %T, want int or int32", got)
		return false
	}
	return true
}

func TestWriteGeminiDiscoveryAvro_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "discovery.avro")

	rec := GeminiDiscoveryRecord{
		CAMIS:        "12345678",
		DBA:          "Test Pizza Place",
		Prompt:       "Find the menu URL",
		ResponseText: "https://testpizza.com/menu",
		SourceURLs:   []string{"https://testpizza.com/menu"},
		Model:        "gemini-2.5-flash",
		EventID:      uuid.NewString(),
		JobID:        "42",
		Attempt:      1,
	}

	if err := WriteGeminiDiscoveryAvro(context.Background(), dest, rec); err != nil {
		t.Fatalf("WriteGeminiDiscoveryAvro: %v", err)
	}

	f, err := os.Open(dest)
	if err != nil {
		t.Fatalf("open avro: %v", err)
	}
	defer func() { _ = f.Close() }()

	dec, err := ocf.NewDecoder(f)
	if err != nil {
		t.Fatalf("new decoder: %v", err)
	}

	var rows []map[string]any
	for dec.HasNext() {
		var row map[string]any
		if err := dec.Decode(&row); err != nil {
			t.Fatalf("decode: %v", err)
		}
		rows = append(rows, row)
	}
	if err := dec.Error(); err != nil {
		t.Fatalf("decoder error: %v", err)
	}

	if len(rows) != 1 {
		t.Fatalf("expected 1 record, got %d", len(rows))
	}
	row := rows[0]

	if row["camis"] != rec.CAMIS {
		t.Errorf("camis = %v, want %q", row["camis"], rec.CAMIS)
	}
	if row["event_id"] != rec.EventID {
		t.Errorf("event_id = %v, want %q", row["event_id"], rec.EventID)
	}
	if row["job_id"] != rec.JobID {
		t.Errorf("job_id = %v, want %q", row["job_id"], rec.JobID)
	}
	if !assertAttempt(t, row["attempt"], rec.Attempt) {
		t.Errorf("attempt mismatch")
	}
}

func TestWriteMenuExtractionAvro_Roundtrip_DiscoveryEventID(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "extraction.avro")

	discoveryEventID := uuid.NewString()
	price := 12.5
	modPrice := 2.0
	rec := MenuExtractionRecord{
		BusinessID:     "550e8400-e29b-41d4-a716-446655440000",
		SourceURL:      "https://testpizza.com/menu",
		RestaurantName: "Test Pizza Place",
		Items: []search.MenuItem{
			{
				MenuItemID:         "item-uuid-1",
				DishName:           "Margherita",
				Description:        "Classic tomato and mozzarella",
				MenuSection:        "Pizzas",
				Price:              &price,
				StatedIngredients:  []string{"tomato", "mozzarella"},
				HasFullIngredients: true,
				Modifiers:          []search.Modifier{{Name: "Large", Price: &modPrice}},
				SourceURL:          "https://testpizza.com/menu",
				ScrapedAt:          "2026-06-30T00:00:00Z",
			},
		},
		EventID:          uuid.NewString(),
		JobID:            "99",
		Attempt:          2,
		DiscoveryEventID: discoveryEventID,
	}

	if err := WriteMenuExtractionAvro(context.Background(), dest, rec); err != nil {
		t.Fatalf("WriteMenuExtractionAvro: %v", err)
	}

	f, err := os.Open(dest)
	if err != nil {
		t.Fatalf("open avro: %v", err)
	}
	defer func() { _ = f.Close() }()

	dec, err := ocf.NewDecoder(f)
	if err != nil {
		t.Fatalf("new decoder: %v", err)
	}

	var rows []map[string]any
	for dec.HasNext() {
		var row map[string]any
		if err := dec.Decode(&row); err != nil {
			t.Fatalf("decode: %v", err)
		}
		rows = append(rows, row)
	}
	if err := dec.Error(); err != nil {
		t.Fatalf("decoder error: %v", err)
	}

	if len(rows) != 1 {
		t.Fatalf("expected 1 record, got %d", len(rows))
	}
	row := rows[0]

	if row["discovery_event_id"] != discoveryEventID {
		t.Errorf("discovery_event_id = %v, want %q", row["discovery_event_id"], discoveryEventID)
	}
	if row["business_id"] != rec.BusinessID {
		t.Errorf("business_id = %v, want %q", row["business_id"], rec.BusinessID)
	}
	if !assertAttempt(t, row["attempt"], rec.Attempt) {
		t.Errorf("attempt mismatch")
	}

	// Per-item fields must survive roundtrip so Avro replay can restore the
	// menu_items table bit-for-bit.
	items, ok := row["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("items: expected 1-element array, got %T len=%v", row["items"], len(row["items"].([]any)))
	}
	item := items[0].(map[string]any)
	if item["menu_item_id"] != "item-uuid-1" {
		t.Errorf("menu_item_id = %v, want item-uuid-1", item["menu_item_id"])
	}
	if item["menu_section"] != "Pizzas" {
		t.Errorf("menu_section = %v, want Pizzas", item["menu_section"])
	}
	if p, ok := item["price"].(float64); !ok || p != price {
		t.Errorf("price = %v, want %v", item["price"], price)
	}
	mods, _ := item["modifiers"].([]any)
	if len(mods) != 1 {
		t.Fatalf("modifiers: expected 1, got %d", len(mods))
	}
	mod := mods[0].(map[string]any)
	if mod["name"] != "Large" {
		t.Errorf("modifier name = %v, want Large", mod["name"])
	}
	if mp, ok := mod["price"].(float64); !ok || mp != modPrice {
		t.Errorf("modifier price = %v, want %v", mod["price"], modPrice)
	}
	if item["source_url"] != "https://testpizza.com/menu" {
		t.Errorf("source_url = %v", item["source_url"])
	}
	if item["scraped_at"] != "2026-06-30T00:00:00Z" {
		t.Errorf("scraped_at = %v", item["scraped_at"])
	}
}

func TestWriteGeminiDiscoveryAvro_EmptySourceURLs(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "discovery_empty.avro")

	rec := GeminiDiscoveryRecord{
		CAMIS:        "00000001",
		DBA:          "No Website Diner",
		Prompt:       "Find menu",
		ResponseText: "I could not find a website.",
		SourceURLs:   nil, // no URLs found
		Model:        "gemini-2.5-flash",
		EventID:      uuid.NewString(),
		JobID:        "1",
		Attempt:      1,
	}

	// Should not error — nil SourceURLs is coerced to empty slice in the writer.
	if err := WriteGeminiDiscoveryAvro(context.Background(), dest, rec); err != nil {
		t.Fatalf("WriteGeminiDiscoveryAvro with nil SourceURLs: %v", err)
	}
}

// ── ScrapeMenuWorker helpers ──────────────────────────────────────────────────

func TestScrapeMenuWorker_BronzeDir_Field(t *testing.T) {
	t.Setenv("RESTAURANT_BRONZE_DIR", "")
	w := &ScrapeMenuWorker{BronzeDir: "/custom/bronze"}
	if got := w.bronzeDir(); got != "/custom/bronze" {
		t.Errorf("bronzeDir() = %q, want /custom/bronze", got)
	}
}

func TestScrapeMenuWorker_BronzeDir_Env(t *testing.T) {
	t.Setenv("RESTAURANT_BRONZE_DIR", "/env/bronze")
	w := &ScrapeMenuWorker{}
	if got := w.bronzeDir(); got != "/env/bronze" {
		t.Errorf("bronzeDir() = %q, want /env/bronze", got)
	}
}

func TestScrapeMenuWorker_BronzeDir_Default(t *testing.T) {
	t.Setenv("RESTAURANT_BRONZE_DIR", "")
	w := &ScrapeMenuWorker{}
	if got := w.bronzeDir(); got != "data/bronze/restaurants" {
		t.Errorf("bronzeDir() = %q, want data/bronze/restaurants", got)
	}
}

func TestScrapeMenuWorker_Timeout(t *testing.T) {
	w := &ScrapeMenuWorker{}
	if got := w.Timeout(nil); got != 5*time.Minute {
		t.Errorf("Timeout() = %v, want 5m", got)
	}
}
