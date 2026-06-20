// Package menutracking implements the regulatory tracking pipeline: periodic
// scraping of government and consultancy sources, LLM-assisted extraction,
// rule quarantine, and structured upsert into Postgres and Weaviate.
package menutracking

import (
	"encoding/json"

	"github.com/invopop/jsonschema"
)

// ChangeType describes the kind of regulatory change observed.
type ChangeType string

// ChangeType values describe the kind of regulatory change observed.
const (
	ChangeTypeAddition    ChangeType = "addition"
	ChangeTypeRestriction ChangeType = "restriction"
	ChangeTypeRevocation  ChangeType = "revocation"
	ChangeTypeUpdate      ChangeType = "update"
)

// StructuredUpdate is the flat JSON object produced by the extraction step
// (fast-path rule or agent path). It is designed without $ref or oneOf so
// that Gemini's ResponseSchema can represent it directly.
type StructuredUpdate struct {
	CASNumber     string     `json:"cas_number" jsonschema:"description=CAS Registry Number, empty string if not applicable"`
	SubstanceName string     `json:"substance_name" jsonschema:"description=Name of the regulated substance,required"`
	ChangeType    ChangeType `json:"change_type" jsonschema:"description=Type of regulatory change,enum=addition,enum=restriction,enum=revocation,enum=update,required"`
	Description   string     `json:"description" jsonschema:"description=Plain-text summary of the regulatory change,required"`
	EffectiveDate string     `json:"effective_date" jsonschema:"description=ISO 8601 date when the change takes effect, empty if unknown"`
	SourceURL     string     `json:"source_url" jsonschema:"description=URL of the page this was extracted from,required"`
}

// StructuredUpdateSchema returns the JSON Schema for StructuredUpdate as a
// map suitable for passing to Gemini's ResponseSchema. ExpandedStruct is
// enabled to flatten any $ref — Gemini's structured output does not support
// $ref.
//
// The panics on marshal/unmarshal errors are safe because the input is a
// reflection-generated schema from a static Go struct — json.Marshal of a
// *jsonschema.Schema always produces valid JSON, and the round-trip through
// map[string]any is infallible for that output. This follows the same
// convention as regexp.MustCompile on a compile-time constant.
func StructuredUpdateSchema() map[string]any {
	r := &jsonschema.Reflector{
		ExpandedStruct: true,
	}
	s := r.Reflect(&StructuredUpdate{})
	b, err := json.Marshal(s)
	if err != nil {
		panic("menutracking: marshaling StructuredUpdate schema: " + err.Error())
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		panic("menutracking: unmarshaling StructuredUpdate schema: " + err.Error())
	}
	return m
}
