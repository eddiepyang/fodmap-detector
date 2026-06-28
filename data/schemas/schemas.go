package schemas

// EventSchema is the Avro OCF schema for streaming review records.
var EventSchema = `{
	"type": "record",
	"name": "yelp_reviews",
	"fields": [
		{"name": "review_id", "type": "string"},
		{"name": "user_id", "type": "string"},
		{"name": "business_id", "type": "string"},
		{"name": "stars", "type": "float"},
		{"name": "useful", "type": "float"},
		{"name": "funny", "type": "float"},
		{"name": "cool", "type": "float"},
		{"name": "text", "type": "string"}
	]
}`

// Business holds metadata for a Yelp business record.
type Business struct {
	BusinessID string `json:"business_id"`
	Name       string `json:"name"`
	City       string `json:"city"`
	State      string `json:"state"`
	Categories string `json:"categories"` // comma-separated, e.g. "Italian, Pizza, Restaurants"
}

// Review holds a single Yelp review record as stored in the archive.
type Review struct {
	ReviewID   string  `parquet:"name=review_id, inname=review_id, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY" json:"review_id"`
	UserID     string  `parquet:"name=user_id, inname=user_id, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY" json:"user_id"`
	BusinessID string  `parquet:"name=business_id, inname=business_id, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY" json:"business_id"`
	Stars      float32 `parquet:"name=stars, inname=stars, type=FLOAT"`
	Useful     int32   `parquet:"name=useful, inname=useful, type=INT32"`
	Funny      int32   `parquet:"name=funny, inname=funny, type=INT32"`
	Cool       int32   `parquet:"name=cool, inname=cool, type=INT32"`
	Text       string  `parquet:"name=text, inname=text, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
}

// NYCRestaurantSchema is the Avro schema for Socrata CSV ingestion.
var NYCRestaurantSchema = `{
    "type": "record",
    "name": "nyc_restaurant",
    "fields": [
        {"name": "camis", "type": "string"},
        {"name": "dba", "type": "string"},
        {"name": "boro", "type": "string"},
        {"name": "building", "type": "string"},
        {"name": "street", "type": "string"},
        {"name": "zipcode", "type": "string"},
        {"name": "phone", "type": "string"},
        {"name": "cuisine_description", "type": "string"},
        {"name": "inspection_date", "type": "string"},
        {"name": "latitude", "type": "double"},
        {"name": "longitude", "type": "double"},
        {"name": "nta", "type": "string"},
        {"name": "record_date", "type": "string"},
        {"name": "event_id", "type": "string"},
        {"name": "created_at", "type": "string"}
    ]
}`

// GeminiDiscoverySchema is the Avro schema for Gemini discovery results.
var GeminiDiscoverySchema = `{
    "type": "record",
    "name": "gemini_discovery",
    "fields": [
        {"name": "camis", "type": "string"},
        {"name": "dba", "type": "string"},
        {"name": "prompt", "type": "string"},
        {"name": "response_text", "type": "string"},
        {"name": "source_urls", "type": {"type": "array", "items": "string"}},
        {"name": "model", "type": "string"},
        {"name": "event_id", "type": "string"},
        {"name": "job_id", "type": "string"},
        {"name": "attempt", "type": "int"},
        {"name": "created_at", "type": "string"}
    ]
}`

// MenuExtractionSchema is the Avro schema for post-LLM scraped menu results.
var MenuExtractionSchema = `{
    "type": "record",
    "name": "menu_extraction",
    "fields": [
        {"name": "camis", "type": "string"},
        {"name": "source_url", "type": "string"},
        {"name": "restaurant_name", "type": "string"},
        {"name": "items", "type": {
            "type": "array",
            "items": {
                "type": "record",
                "name": "menu_item",
                "fields": [
                    {"name": "dish_name", "type": "string"},
                    {"name": "description", "type": "string"},
                    {"name": "stated_ingredients", "type": {"type": "array", "items": "string"}},
                    {"name": "has_full_ingredients", "type": "boolean"}
                ]
            }
        }},
        {"name": "event_id", "type": "string"},
        {"name": "job_id", "type": "string"},
        {"name": "attempt", "type": "int"},
        {"name": "discovery_event_id", "type": "string"},
        {"name": "extraction_tier", "type": "string", "default": ""},
        {"name": "created_at", "type": "string"}
    ]
}`
