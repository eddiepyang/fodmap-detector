# Data Model & Pipeline

## Core Data Model

Reviews reference businesses by ID only. The business name and location metadata live in a separate dataset file.

```go
// Review holds a single review record. BusinessID is a foreign key into the business dataset —
// the business name is NOT present here.
type Review struct {
    ReviewID   string  // Yelp review ID
    UserID     string  // Reviewer user ID
    BusinessID string  // Foreign key — look up name/location in Business
    Stars      float32 // Rating (1-5)
    Useful     int32   // Usefulness votes
    Funny      int32   // Funny votes
    Cool       int32   // Cool votes
    Text       string  // Full review text
}

// Business holds metadata from yelp_academic_dataset_business.json.
// Required to resolve a BusinessID to a human-readable name.
type Business struct {
    BusinessID string // Primary key, matches Review.BusinessID
    Name       string // Human-readable restaurant/business name
    City       string
    State      string
    Categories string // Comma-separated, e.g. "Italian, Pizza, Restaurants"
}
```

The FODMAP ingredient database (`data/fodmap.go`) contains 100+ entries with FODMAP level, group tags, notes, and substitution suggestions:

```go
type FodmapEntry struct {
    Level         string   `json:"level"`                    // "high", "moderate", or "low"
    Groups        []string `json:"groups"`                   // FODMAP groups, e.g. ["fructan", "mannitol"]
    Notes         string   `json:"notes,omitempty"`          // Additional context (serving thresholds, etc.)
    Substitutions []string `json:"substitutions,omitempty"`  // Low-FODMAP alternatives for high/moderate ingredients
}
```

When the chat agent looks up a high or moderate FODMAP ingredient, it automatically presents the substitution suggestions to the user as practical alternatives.

The Avro streaming schema (`EventSchema`) mirrors the `Review` struct and carries `business_id` but not the business name. During indexing, the name is joined from the business dataset and stored in Weaviate so search results include it directly.

---

## Data Pipeline

```
data/archive.tar.gz  (Yelp JSON lines, gzip-compressed)
        |
        v
   GetArchive(path, "review")  ->  *bufio.Scanner
        |
   |
Avro path (event cmd)
   |
EventWriter.Write()
   |
*.avro
```

---

## Input Data

Place the Yelp dataset archive at:

```
./data/archive.tar.gz
```

The archive must contain files whose names include `"review"` and `"business"`:
- `yelp_academic_dataset_review.json` — review text and ratings (required for all features)
- `yelp_academic_dataset_business.json` — business name, city, state, categories (required for search filters)

Both files must be formatted as newline-delimited JSON (JSONL).

