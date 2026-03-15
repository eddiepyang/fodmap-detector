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
