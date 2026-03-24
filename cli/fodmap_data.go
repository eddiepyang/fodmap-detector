package cli

import "strings"

// fodmapEntry describes the FODMAP classification for an ingredient.
type fodmapEntry struct {
	Level  string   // "high", "moderate", "low"
	Groups []string // FODMAP groups present
	Notes  string
}

// fodmapDB is a curated static lookup of common ingredients.
// Sources: Monash University FODMAP guide; Stanford low-FODMAP diet resources.
// This is an approximation — users should consult the official Monash app for
// certified serving-size thresholds.
var fodmapDB = map[string]fodmapEntry{
	// High FODMAP — Fructans
	"garlic":    {Level: "high", Groups: []string{"fructans"}, Notes: "Even small amounts are high FODMAP; garlic-infused oil is low FODMAP"},
	"onion":     {Level: "high", Groups: []string{"fructans"}, Notes: "One of the highest FODMAP foods"},
	"leek":      {Level: "high", Groups: []string{"fructans"}, Notes: "Leek leaves are lower FODMAP than the bulb"},
	"shallot":   {Level: "high", Groups: []string{"fructans"}},
	"wheat":     {Level: "high", Groups: []string{"fructans"}, Notes: "Includes bread, pasta, flour"},
	"rye":       {Level: "high", Groups: []string{"fructans"}},
	"barley":    {Level: "high", Groups: []string{"fructans"}},
	"asparagus": {Level: "high", Groups: []string{"fructans"}},
	"artichoke": {Level: "high", Groups: []string{"fructans"}},

	// High FODMAP — GOS
	"beans":      {Level: "high", Groups: []string{"GOS"}, Notes: "Kidney, black, baked beans"},
	"lentils":    {Level: "high", Groups: []string{"GOS"}},
	"chickpeas":  {Level: "high", Groups: []string{"GOS"}},
	"cashews":    {Level: "high", Groups: []string{"GOS"}},
	"pistachios": {Level: "high", Groups: []string{"GOS"}},

	// High FODMAP — Lactose
	"milk":        {Level: "high", Groups: []string{"lactose"}, Notes: "Cow, goat, sheep milk; lactose-free milk is low FODMAP"},
	"yogurt":      {Level: "high", Groups: []string{"lactose"}, Notes: "Regular yogurt; lactose-free yogurt is low FODMAP"},
	"soft cheese": {Level: "high", Groups: []string{"lactose"}, Notes: "Ricotta, cottage, cream cheese"},
	"ice cream":   {Level: "high", Groups: []string{"lactose"}},
	"cream":       {Level: "high", Groups: []string{"lactose"}},
	"custard":     {Level: "high", Groups: []string{"lactose"}},

	// High FODMAP — Excess fructose
	"honey":      {Level: "high", Groups: []string{"excess fructose"}},
	"apple":      {Level: "high", Groups: []string{"excess fructose", "sorbitol"}},
	"pear":       {Level: "high", Groups: []string{"excess fructose", "sorbitol"}},
	"mango":      {Level: "high", Groups: []string{"excess fructose"}},
	"watermelon": {Level: "high", Groups: []string{"excess fructose", "mannitol"}},
	"hfcs":       {Level: "high", Groups: []string{"excess fructose"}, Notes: "High fructose corn syrup"},

	// High FODMAP — Sorbitol
	"avocado":     {Level: "high", Groups: []string{"sorbitol"}, Notes: "1/8 of avocado is low FODMAP"},
	"peach":       {Level: "high", Groups: []string{"sorbitol"}},
	"plum":        {Level: "high", Groups: []string{"sorbitol"}},
	"apricot":     {Level: "high", Groups: []string{"sorbitol"}},
	"cherry":      {Level: "high", Groups: []string{"sorbitol"}},
	"nectarine":   {Level: "high", Groups: []string{"sorbitol"}},
	"blackberries": {Level: "high", Groups: []string{"sorbitol"}},

	// High FODMAP — Mannitol
	"mushrooms":   {Level: "high", Groups: []string{"mannitol"}},
	"cauliflower": {Level: "high", Groups: []string{"mannitol"}},
	"celery":      {Level: "high", Groups: []string{"mannitol"}},

	// Moderate FODMAP
	"peas":    {Level: "moderate", Groups: []string{"GOS", "mannitol"}},
	"beetroot": {Level: "moderate", Groups: []string{"fructans"}},
	"coconut cream": {Level: "moderate", Groups: []string{"sorbitol"}, Notes: "Coconut milk (½ cup canned) is low FODMAP"},

	// Low FODMAP
	"rice":         {Level: "low", Groups: []string{}},
	"oats":         {Level: "low", Groups: []string{}, Notes: "Up to ½ cup; larger servings may contain fructans"},
	"quinoa":       {Level: "low", Groups: []string{}},
	"corn":         {Level: "low", Groups: []string{}, Notes: "Up to ½ cup canned; corn flour is higher"},
	"potato":       {Level: "low", Groups: []string{}},
	"carrot":       {Level: "low", Groups: []string{}},
	"cucumber":     {Level: "low", Groups: []string{}},
	"lettuce":      {Level: "low", Groups: []string{}},
	"spinach":      {Level: "low", Groups: []string{}},
	"tomato":       {Level: "low", Groups: []string{}, Notes: "Up to 3 cherry tomatoes or ½ common tomato"},
	"bell pepper":  {Level: "low", Groups: []string{}},
	"zucchini":     {Level: "low", Groups: []string{}},
	"eggplant":     {Level: "low", Groups: []string{}},
	"kale":         {Level: "low", Groups: []string{}},
	"bok choy":     {Level: "low", Groups: []string{}},
	"chicken":      {Level: "low", Groups: []string{}},
	"beef":         {Level: "low", Groups: []string{}},
	"pork":         {Level: "low", Groups: []string{}},
	"fish":         {Level: "low", Groups: []string{}},
	"shrimp":       {Level: "low", Groups: []string{}},
	"eggs":         {Level: "low", Groups: []string{}},
	"tofu":         {Level: "low", Groups: []string{}, Notes: "Firm tofu; silken tofu is high GOS"},
	"hard cheese":  {Level: "low", Groups: []string{}, Notes: "Lactose is negligible in aged hard cheeses"},
	"cheddar":      {Level: "low", Groups: []string{}},
	"parmesan":     {Level: "low", Groups: []string{}},
	"brie":         {Level: "low", Groups: []string{}, Notes: "Small servings; soft but low lactose"},
	"strawberries": {Level: "low", Groups: []string{}},
	"blueberries":  {Level: "low", Groups: []string{}, Notes: "Up to 20 berries per serving"},
	"orange":       {Level: "low", Groups: []string{}},
	"banana":       {Level: "low", Groups: []string{}, Notes: "Firm/unripe; ripe banana has higher fructans"},
	"grapes":       {Level: "low", Groups: []string{}},
	"pineapple":    {Level: "low", Groups: []string{}},
	"kiwi":         {Level: "low", Groups: []string{}},
	"coconut milk": {Level: "low", Groups: []string{}, Notes: "Up to ½ cup canned"},
	"almond milk":  {Level: "low", Groups: []string{}},
	"olive oil":    {Level: "low", Groups: []string{}},
	"butter":       {Level: "low", Groups: []string{}, Notes: "Lactose is negligible when used in cooking"},
	"soy sauce":    {Level: "low", Groups: []string{}, Notes: "Tamari preferred; regular soy sauce has trace wheat"},
	"sugar":        {Level: "low", Groups: []string{}},
	"maple syrup":  {Level: "low", Groups: []string{}},
	"ginger":       {Level: "low", Groups: []string{}},
	"chili":        {Level: "low", Groups: []string{}},
	"lemon":        {Level: "low", Groups: []string{}},
	"lime":         {Level: "low", Groups: []string{}},
	"vinegar":      {Level: "low", Groups: []string{}},
	"almonds":      {Level: "low", Groups: []string{}, Notes: "Up to 10 almonds; larger amounts contain GOS"},
	"walnuts":      {Level: "low", Groups: []string{}, Notes: "Up to 10 walnut halves"},
	"peanuts":      {Level: "low", Groups: []string{}},
	"tempeh":       {Level: "low", Groups: []string{}},
}

// lookupFODMAP returns the FODMAP classification for an ingredient.
// Performs case-insensitive exact matching, then partial/substring matching.
func lookupFODMAP(ingredient string) map[string]any {
	key := strings.ToLower(strings.TrimSpace(ingredient))

	if entry, ok := fodmapDB[key]; ok {
		return fodmapResult(key, entry)
	}

	// Partial match: check if any known key is a substring of the ingredient or vice versa.
	for k, entry := range fodmapDB {
		if strings.Contains(key, k) || strings.Contains(k, key) {
			return fodmapResult(k, entry)
		}
	}

	return map[string]any{
		"ingredient": ingredient,
		"found":      false,
		"message":    "ingredient not in database; consult the Monash University FODMAP app for accurate classification",
	}
}

func fodmapResult(name string, e fodmapEntry) map[string]any {
	result := map[string]any{
		"ingredient":    name,
		"found":         true,
		"fodmap_level":  e.Level,
		"fodmap_groups": e.Groups,
	}
	if e.Notes != "" {
		result["notes"] = e.Notes
	}
	return result
}
