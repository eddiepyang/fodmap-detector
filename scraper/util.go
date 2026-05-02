package scraper

import (
	"encoding/json"
	"strings"
)

// decodeJSON decodes a JSON string into v. Used by extractor.go.
func decodeJSON(s string, v interface{}) error {
	return json.NewDecoder(strings.NewReader(s)).Decode(v)
}
