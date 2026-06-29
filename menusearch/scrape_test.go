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
	rec := MenuExtractionRecord{
		CAMIS:          "12345678",
		SourceURL:      "https://testpizza.com/menu",
		RestaurantName: "Test Pizza Place",
		Items: []search.MenuItem{
			{
				DishName:           "Margherita",
				Description:        "Classic tomato and mozzarella",
				StatedIngredients:  []string{"tomato", "mozzarella"},
				HasFullIngredients: true,
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
	if row["camis"] != rec.CAMIS {
		t.Errorf("camis = %v, want %q", row["camis"], rec.CAMIS)
	}
	if !assertAttempt(t, row["attempt"], rec.Attempt) {
		t.Errorf("attempt mismatch")
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
