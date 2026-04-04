package data

// FodmapEntry describes the FODMAP classification for an ingredient.
type FodmapEntry struct {
	Level  string   `json:"level"`  // "high", "moderate", "low"
	Groups []string `json:"groups"` // FODMAP groups present
	Notes  string   `json:"notes,omitempty"`
}

// FodmapDB is a curated static lookup of common ingredients.
var FodmapDB = map[string]FodmapEntry{
	"garlic":        {Level: "high", Groups: []string{"fructans"}, Notes: "Even small amounts are high FODMAP; garlic-infused oil is low FODMAP"},
	"onion":         {Level: "high", Groups: []string{"fructans"}, Notes: "One of the highest FODMAP foods"},
	"wheat":         {Level: "high", Groups: []string{"fructans"}, Notes: "Includes bread, pasta, flour"},
	"milk":          {Level: "high", Groups: []string{"lactose"}, Notes: "Cow, goat, sheep milk; lactose-free milk is low FODMAP"},
	"apple":         {Level: "high", Groups: []string{"excess fructose", "sorbitol"}},
	"avocado":       {Level: "high", Groups: []string{"sorbitol"}, Notes: "1/8 of avocado is low FODMAP"},
	"peas":          {Level: "moderate", Groups: []string{"GOS", "mannitol"}},
	"coconut cream": {Level: "moderate", Groups: []string{"sorbitol"}, Notes: "Coconut milk (½ cup canned) is low FODMAP"},
	"rice":          {Level: "low", Groups: []string{}},
	"tomato":        {Level: "low", Groups: []string{}, Notes: "Up to 3 cherry tomatoes or ½ common tomato"},
	"chicken":       {Level: "low", Groups: []string{}},
	"tofu":          {Level: "low", Groups: []string{}, Notes: "Firm tofu; silken tofu is high GOS"},
	"hard cheese":   {Level: "low", Groups: []string{}, Notes: "Lactose is negligible in aged hard cheeses"},
}
