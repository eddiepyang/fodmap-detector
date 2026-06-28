package menusearch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"fodmap/data/io"
	"fodmap/data/schemas"
	"fodmap/search"

	"github.com/google/uuid"
	"github.com/hamba/avro/v2/ocf"
)

// WriteNYCRestaurantAvro writes a batch of NYC restaurant records to the bronze layer.
func WriteNYCRestaurantAvro(ctx context.Context, destPath string, records []NYCRestaurantRecord) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}

	writer, err := io.NewEventWriter(f, schemas.NYCRestaurantSchema)
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("new event writer: %w", err)
	}
	defer func() { _ = writer.Close() }()

	now := time.Now().UTC().Format(time.RFC3339)

	for _, rec := range records {
		record := map[string]any{
			"camis":               rec.CAMIS,
			"dba":                 rec.DBA,
			"boro":                rec.Boro,
			"building":            rec.Building,
			"street":              rec.Street,
			"zipcode":             rec.Zipcode,
			"phone":               rec.Phone,
			"cuisine_description": rec.CuisineDescription,
			"inspection_date":     rec.InspectionDate,
			"latitude":            rec.Latitude,
			"longitude":           rec.Longitude,
			"nta":                 rec.NTA,
			"record_date":         rec.RecordDate,
			"event_id":            uuid.NewString(),
			"created_at":          now,
		}
		if err := writer.WriteRaw(record); err != nil {
			return fmt.Errorf("write raw: %w", err)
		}
	}

	return nil
}

type GeminiDiscoveryRecord struct {
	CAMIS        string
	DBA          string
	Prompt       string
	ResponseText string
	SourceURLs   []string
	Model        string
	EventID      string
	JobID        string
	Attempt      int
}

// WriteGeminiDiscoveryAvro writes a single Gemini discovery record to the bronze layer.
func WriteGeminiDiscoveryAvro(ctx context.Context, destPath string, rec GeminiDiscoveryRecord) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}

	writer, err := io.NewEventWriter(f, schemas.GeminiDiscoverySchema)
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("new event writer: %w", err)
	}
	defer func() { _ = writer.Close() }()

	now := time.Now().UTC().Format(time.RFC3339)
	urls := rec.SourceURLs
	if urls == nil {
		urls = []string{}
	}

	record := map[string]any{
		"camis":         rec.CAMIS,
		"dba":           rec.DBA,
		"prompt":        rec.Prompt,
		"response_text": rec.ResponseText,
		"source_urls":   urls,
		"model":         rec.Model,
		"event_id":      rec.EventID,
		"job_id":        rec.JobID,
		"attempt":       rec.Attempt,
		"created_at":    now,
	}

	return writer.Write(record)
}

type MenuExtractionRecord struct {
	CAMIS            string
	SourceURL        string
	RestaurantName   string
	Items            []search.MenuItem
	EventID          string
	JobID            string
	Attempt          int
	DiscoveryEventID string
	ExtractionTier   string // cascade tier that produced the result (pipeline.Tier*)
}

// WriteMenuExtractionAvro writes a single menu extraction record to the bronze layer.
func WriteMenuExtractionAvro(ctx context.Context, destPath string, rec MenuExtractionRecord) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}

	writer, err := io.NewEventWriter(f, schemas.MenuExtractionSchema)
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("new event writer: %w", err)
	}
	defer func() { _ = writer.Close() }()

	now := time.Now().UTC().Format(time.RFC3339)
	items := make([]map[string]any, 0, len(rec.Items))
	for _, item := range rec.Items {
		stated := item.StatedIngredients
		if stated == nil {
			stated = []string{}
		}
		items = append(items, map[string]any{
			"dish_name":            item.DishName,
			"description":          item.Description,
			"stated_ingredients":   stated,
			"has_full_ingredients": item.HasFullIngredients,
		})
	}

	record := map[string]any{
		"camis":              rec.CAMIS,
		"source_url":         rec.SourceURL,
		"restaurant_name":    rec.RestaurantName,
		"items":              items,
		"event_id":           rec.EventID,
		"job_id":             rec.JobID,
		"attempt":            rec.Attempt,
		"discovery_event_id": rec.DiscoveryEventID,
		"extraction_tier":    rec.ExtractionTier,
		"created_at":         now,
	}

	return writer.Write(record)
}

// ReadNYCRestaurantAvro reads a batch of NYC restaurant records from the bronze layer.
func ReadNYCRestaurantAvro(path string) ([]NYCRestaurantRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	decoder, err := ocf.NewDecoder(f)
	if err != nil {
		return nil, err
	}

	var records []NYCRestaurantRecord
	for decoder.HasNext() {
		var record map[string]any
		if err := decoder.Decode(&record); err != nil {
			return nil, err
		}

		rec := NYCRestaurantRecord{
			CAMIS:              safeString(record["camis"]),
			DBA:                safeString(record["dba"]),
			Boro:               safeString(record["boro"]),
			Building:           safeString(record["building"]),
			Street:             safeString(record["street"]),
			Zipcode:            safeString(record["zipcode"]),
			Phone:              safeString(record["phone"]),
			CuisineDescription: safeString(record["cuisine_description"]),
			InspectionDate:     safeString(record["inspection_date"]),
			Latitude:           safeFloat64(record["latitude"]),
			Longitude:          safeFloat64(record["longitude"]),
			NTA:                safeString(record["nta"]),
			RecordDate:         safeString(record["record_date"]),
		}
		records = append(records, rec)
	}

	return records, decoder.Error()
}

func safeString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func safeFloat64(v any) float64 {
	switch f := v.(type) {
	case float64:
		return f
	case float32:
		return float64(f)
	}
	return 0
}
