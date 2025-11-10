package schemas

// type ReviewSchemaS struct {
// 	ReviewId   string  `parquet:"name=review_id, inname=review_id, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY" json:"review_id"`
// 	UserId     string  `parquet:"name=user_id, inname=user_id, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY" json:"user_id"`
// 	BusinessId string  `parquet:"name=business_id, inname=business_id, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY" json:"business_id"`
// 	Stars      float32 `parquet:"name=stars, inname=stars, type=FLOAT"`
// 	Useful     int32   `parquet:"name=useful, inname=useful, type=INT32"`
// 	Funny      int32   `parquet:"name=funny, inname=funny, type=INT32"`
// 	Cool       int32   `parquet:"name=cool, inname=cool, type=INT32"`
// 	Text       string  `parquet:"name=text, inname=text, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
// 	// ParsedText []string `parquet:"name=parsed_text, inname=parsed_text, type=LIST, convertedtype=LIST, valuetype=BYTE_ARRAY, valueconvertedtype=UTF8"`
// }

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

type ReviewSchemaS struct {
	ReviewId   string  `parquet:"name=review_id, inname=review_id, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY" json:"review_id"`
	UserId     string  `parquet:"name=user_id, inname=user_id, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY" json:"user_id"`
	BusinessId string  `parquet:"name=business_id, inname=business_id, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY" json:"business_id"`
	Stars      float32 `parquet:"name=stars, inname=stars, type=FLOAT"`
	Useful     int32   `parquet:"name=useful, inname=useful, type=INT32"`
	Funny      int32   `parquet:"name=funny, inname=funny, type=INT32"`
	Cool       int32   `parquet:"name=cool, inname=cool, type=INT32"`
	Text       string  `parquet:"name=text, inname=text, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	// ParsedText []string `parquet:"name=parsed_text, inname=parsed_text, type=LIST, convertedtype=LIST, valuetype=BYTE_ARRAY, valueconvertedtype=UTF8"`
}
