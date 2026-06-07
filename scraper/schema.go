package scraper

import (
	"encoding/json"
	"fmt"

	"github.com/invopop/jsonschema"
)

// llmMenuPayload is the schema the LLM must emit. It deliberately omits
// source_url and scraped_at_utc, which the caller fills in after extraction.
type llmMenuPayload struct {
	RestaurantName string      `json:"restaurant_name" jsonschema:"required"`
	City           string      `json:"city,omitempty"`
	State          string      `json:"state,omitempty"`
	Items          []MenuEntry `json:"items" jsonschema:"required"`
}

// menuExtractionSchema returns a JSON Schema for llmMenuPayload, suitable for
// use in an OpenAI response_format.json_schema payload. The schema is inlined
// (ExpandedStruct: true) so the root struct is not a $ref. Nested types retain
// $ref/$defs, which Gemini's OpenAI-compat endpoint accepts (verified).
func menuExtractionSchema() (json.RawMessage, error) {
	r := jsonschema.Reflector{
		ExpandedStruct:            true,
		AllowAdditionalProperties: false,
	}
	s := r.Reflect(&llmMenuPayload{})
	b, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("marshal menu schema: %w", err)
	}
	return b, nil
}
