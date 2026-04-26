package data

// FodmapEntry describes the FODMAP classification for an ingredient.
type FodmapEntry struct {
	Level         string   `json:"level"`                   // "high", "moderate", "low"
	Groups        []string `json:"groups"`                  // FODMAP groups present
	Notes         string   `json:"notes,omitempty"`         // serving-size guidance, preparation notes
	Substitutions []string `json:"substitutions,omitempty"` // low-FODMAP alternatives for high/moderate ingredients
}

// FodmapDB is a curated static lookup of common ingredients based on Monash
// University FODMAP research. Levels are per standard serving sizes unless
// noted otherwise in the Notes field.
var FodmapDB = map[string]FodmapEntry{
	// ---- HIGH FODMAP ----

	// Fructans
	"garlic": {
		Level:         "high",
		Groups:        []string{"fructans"},
		Notes:         "Even small amounts are high FODMAP; garlic-infused oil is low FODMAP because fructans are not oil-soluble",
		Substitutions: []string{"garlic-infused olive oil", "garlic chives", "asafoetida powder (small amount, resin form)"},
	},
	"onion": {
		Level:         "high",
		Groups:        []string{"fructans"},
		Notes:         "One of the highest FODMAP foods; all varieties (red, white, spring bulb) are high",
		Substitutions: []string{"spring onion greens (green part only)", "chives", "asafoetida powder (small amount)"},
	},
	"wheat": {
		Level:         "high",
		Groups:        []string{"fructans"},
		Notes:         "Includes bread, pasta, flour; spelt has lower fructan content but is still moderate",
		Substitutions: []string{"sourdough bread (long-fermented)", "gluten-free bread", "rice bread", "oat flour"},
	},
	"rye": {
		Level:         "high",
		Groups:        []string{"fructans"},
		Notes:         "Rye bread, rye crackers, and pumpernickel are all high FODMAP",
		Substitutions: []string{"sourdough bread (wheat, long-fermented)", "gluten-free crackers", "rice crackers"},
	},
	"barley": {
		Level:         "high",
		Groups:        []string{"fructans"},
		Notes:         "Barley flour, pearl barley, and barley-based drinks are high FODMAP",
		Substitutions: []string{"rice", "quinoa", "millet"},
	},
	"inulin": {
		Level:         "high",
		Groups:        []string{"fructans"},
		Notes:         "Added to many processed foods as a prebiotic fiber; also called chicory root fiber",
		Substitutions: []string{"psyllium husk", "rice bran"},
	},
	"chicory root": {
		Level:         "high",
		Groups:        []string{"fructans"},
		Notes:         "Often used as a coffee substitute or added to processed foods for fiber",
		Substitutions: []string{"dandelion root coffee substitute (check label)", "carob powder"},
	},
	"leek bulb": {
		Level:         "high",
		Groups:        []string{"fructans"},
		Notes:         "The white/light green bulb is high; the dark green leaves are moderate in small amounts",
		Substitutions: []string{"leek greens (dark green part, small amount)", "spring onion greens"},
	},
	"shallot": {
		Level:         "high",
		Groups:        []string{"fructans"},
		Notes:         "Similar fructan profile to onion",
		Substitutions: []string{"spring onion greens", "chives"},
	},
	"beetroot": {
		Level:         "high",
		Groups:        []string{"fructans"},
		Notes:         "High in fructans at standard servings; small amounts (2-3 slices) may be tolerated",
		Substitutions: []string{"carrot", "roasted red pepper"},
	},
	"savoy cabbage": {
		Level:         "high",
		Groups:        []string{"fructans"},
		Notes:         "High at standard servings",
		Substitutions: []string{"common cabbage (small amount)", "bok choy", "spinach"},
	},
	"dandelion greens": {
		Level:         "high",
		Groups:        []string{"fructans"},
		Notes:         "High in both fructans and inulin",
		Substitutions: []string{"arugula", "spinach", "kale"},
	},

	// GOS (galacto-oligosaccharides)
	"chickpeas": {
		Level:         "high",
		Groups:        []string{"GOS"},
		Notes:         "Canned chickpeas are lower GOS than cooked from dry; 1/4 cup canned may be tolerated",
		Substitutions: []string{"canned chickpeas (small amount, rinsed well)", "firm tofu", "tempeh"},
	},
	"lentils": {
		Level:         "high",
		Groups:        []string{"GOS"},
		Notes:         "Canned lentils are lower GOS than cooked from dry; 1/4 cup canned may be tolerated",
		Substitutions: []string{"canned lentils (small amount, rinsed well)", "quinoa", "firm tofu"},
	},
	"kidney beans": {
		Level:         "high",
		Groups:        []string{"GOS"},
		Notes:         "Very high in GOS regardless of preparation method",
		Substitutions: []string{"canned chickpeas (small amount)", "firm tofu"},
	},
	"black beans": {
		Level:         "high",
		Groups:        []string{"GOS"},
		Notes:         "Very high in GOS",
		Substitutions: []string{"canned chickpeas (small amount)", "firm tofu"},
	},
	"baked beans": {
		Level:         "high",
		Groups:        []string{"GOS", "fructans"},
		Notes:         "High in GOS from beans and fructans from onion/garlic in sauce",
		Substitutions: []string{"canned chickpeas (small amount, rinsed)", "firm tofu with garlic-infused oil sauce"},
	},
	"hummus": {
		Level:         "high",
		Groups:        []string{"GOS", "fructans"},
		Notes:         "Contains chickpeas (GOS) and often garlic (fructans); 2 tbsp may be tolerated",
		Substitutions: []string{"hummus made without garlic, using canned chickpeas (small amount)", "tahini dip"},
	},
	"cashews": {
		Level:         "high",
		Groups:        []string{"GOS"},
		Notes:         "High at standard servings; 10 cashews may be tolerated",
		Substitutions: []string{"walnuts (small amount)", "macadamia nuts", "peanuts"},
	},
	"pistachios": {
		Level:         "high",
		Groups:        []string{"GOS"},
		Notes:         "High at standard servings",
		Substitutions: []string{"walnuts", "macadamia nuts", "pecans"},
	},
	"soy milk": {
		Level:         "high",
		Groups:        []string{"GOS"},
		Notes:         "Made from whole soybeans which are high GOS; soy milk from soy protein isolate may be lower",
		Substitutions: []string{"almond milk", "rice milk", "lactose-free milk", "oat milk (small amount)"},
	},

	// Lactose
	"milk": {
		Level:         "high",
		Groups:        []string{"lactose"},
		Notes:         "Cow, goat, sheep milk; lactose-free milk is low FODMAP",
		Substitutions: []string{"lactose-free milk", "almond milk", "rice milk"},
	},
	"yogurt": {
		Level:         "high",
		Groups:        []string{"lactose"},
		Notes:         "Regular yogurt is high lactose; Greek yogurt has less lactose but still high at standard servings",
		Substitutions: []string{"lactose-free yogurt", "coconut yogurt", "almond milk yogurt"},
	},
	"ice cream": {
		Level:         "high",
		Groups:        []string{"lactose"},
		Notes:         "Very high lactose; also often contains high-FODMAP sweeteners",
		Substitutions: []string{"lactose-free ice cream", "coconut ice cream", "sorbet (check for high-FODMAP fruit)"},
	},
	"cottage cheese": {
		Level:         "high",
		Groups:        []string{"lactose"},
		Notes:         "High lactose content",
		Substitutions: []string{"hard cheese", "lactose-free cottage cheese", "ricotta (small amount)"},
	},
	"ricotta": {
		Level:         "high",
		Groups:        []string{"lactose"},
		Notes:         "High lactose at standard servings; 2 tbsp may be tolerated",
		Substitutions: []string{"hard cheese", "lactose-free ricotta", "almond ricotta"},
	},
	"cream cheese": {
		Level:         "high",
		Groups:        []string{"lactose"},
		Notes:         "High lactose at standard servings",
		Substitutions: []string{"hard cheese", "lactose-free cream cheese", "cashew cream (small amount)"},
	},
	"condensed milk": {
		Level:         "high",
		Groups:        []string{"lactose"},
		Notes:         "Extremely high in both lactose and sugar",
		Substitutions: []string{"lactose-free condensed milk", "coconut condensed milk"},
	},
	"evaporated milk": {
		Level:         "high",
		Groups:        []string{"lactose"},
		Notes:         "Concentrated lactose",
		Substitutions: []string{"evaporated lactose-free milk", "coconut cream"},
	},
	"custard": {
		Level:         "high",
		Groups:        []string{"lactose"},
		Notes:         "Made with milk; high lactose",
		Substitutions: []string{"lactose-free custard", "coconut custard", "chia pudding"},
	},
	"cream": {
		Level:         "high",
		Groups:        []string{"lactose"},
		Notes:         "Heavy cream and whipping cream contain lactose; half-and-half is moderate",
		Substitutions: []string{"lactose-free cream", "coconut cream", "dairy-free whipping cream"},
	},

	// Excess fructose
	"apple": {
		Level:         "high",
		Groups:        []string{"excess fructose"},
		Notes:         "High in excess fructose; also contains sorbitol in some varieties",
		Substitutions: []string{"strawberry", "orange", "grape", "kiwi"},
	},
	"pear": {
		Level:         "high",
		Groups:        []string{"excess fructose", "sorbitol"},
		Notes:         "Very high in both excess fructose and sorbitol",
		Substitutions: []string{"strawberry", "orange", "grape", "kiwi"},
	},
	"mango": {
		Level:         "high",
		Groups:        []string{"excess fructose"},
		Notes:         "High at standard servings; 1/2 small mango may be tolerated",
		Substitutions: []string{"papaya", "cantaloupe", "honeydew", "strawberry"},
	},
	"honey": {
		Level:         "high",
		Groups:        []string{"excess fructose"},
		Notes:         "Very high in excess fructose; even small amounts are high FODMAP",
		Substitutions: []string{"pure maple syrup", "table sugar", "rice malt syrup", "golden syrup"},
	},
	"agave": {
		Level:         "high",
		Groups:        []string{"excess fructose"},
		Notes:         "Very high in excess fructose; agave nectar/syrup is not low FODMAP at any serving",
		Substitutions: []string{"pure maple syrup", "table sugar", "rice malt syrup"},
	},
	"high fructose corn syrup": {
		Level:         "high",
		Groups:        []string{"excess fructose"},
		Notes:         "Found in many processed foods and soft drinks",
		Substitutions: []string{"table sugar", "pure maple syrup", "rice malt syrup"},
	},
	"dried fig": {
		Level:         "high",
		Groups:        []string{"excess fructose"},
		Notes:         "Very concentrated fructose source",
		Substitutions: []string{"dried cranberries (small amount)", "dried strawberry"},
	},
	"dried date": {
		Level:         "high",
		Groups:        []string{"excess fructose", "fructans"},
		Notes:         "Very concentrated; high in both excess fructose and fructans",
		Substitutions: []string{"dried cranberries (small amount)", "pure maple syrup"},
	},
	"watermelon": {
		Level:         "high",
		Groups:        []string{"excess fructose", "fructans"},
		Notes:         "High in both excess fructose and fructans",
		Substitutions: []string{"cantaloupe", "honeydew", "strawberry"},
	},
	"cherry": {
		Level:         "high",
		Groups:        []string{"excess fructose", "sorbitol"},
		Notes:         "High at standard servings; 3 cherries may be tolerated",
		Substitutions: []string{"strawberry", "blueberry", "raspberry"},
	},
	"blackberry": {
		Level:         "high",
		Groups:        []string{"excess fructose", "sorbitol"},
		Notes:         "High in both excess fructose and sorbitol",
		Substitutions: []string{"strawberry", "raspberry", "blueberry"},
	},
	"boysenberry": {
		Level:         "high",
		Groups:        []string{"excess fructose", "sorbitol"},
		Notes:         "High in both excess fructose and sorbitol",
		Substitutions: []string{"strawberry", "raspberry", "blueberry"},
	},
	"tamarind": {
		Level:         "high",
		Groups:        []string{"excess fructose", "fructans"},
		Notes:         "High in both excess fructose and fructans",
		Substitutions: []string{"lime juice", "lemon juice", "tamarind-free pad thai sauce"},
	},
	"persimmon": {
		Level:         "high",
		Groups:        []string{"excess fructose"},
		Notes:         "High at standard servings",
		Substitutions: []string{"papaya", "cantaloupe", "orange"},
	},
	"sugar snap pea": {
		Level:         "high",
		Groups:        []string{"excess fructose"},
		Notes:         "High at standard servings; also contains fructans",
		Substitutions: []string{"green beans", "bok choy", "bell pepper"},
	},

	// Polyols (sorbitol)
	"avocado": {
		Level:         "high",
		Groups:        []string{"sorbitol"},
		Notes:         "1/8 of an avocado is low FODMAP; larger servings are high due to sorbitol",
		Substitutions: []string{"avocado (1/8 or less)", "hummus without garlic (small amount)", "olive oil spread"},
	},
	"lychee": {
		Level:         "high",
		Groups:        []string{"excess fructose", "sorbitol"},
		Notes:         "High at standard servings",
		Substitutions: []string{"strawberry", "grape", "orange"},
	},
	"peach": {
		Level:         "high",
		Groups:        []string{"sorbitol"},
		Notes:         "High in sorbitol; canned peach in natural juice may be lower",
		Substitutions: []string{"canned peach (small amount)", "orange", "cantaloupe"},
	},
	"plum": {
		Level:         "high",
		Groups:        []string{"sorbitol"},
		Notes:         "High in sorbitol at standard servings",
		Substitutions: []string{"orange", "strawberry", "cantaloupe"},
	},
	"prunes": {
		Level:         "high",
		Groups:        []string{"sorbitol"},
		Notes:         "Dried plums; very concentrated sorbitol source",
		Substitutions: []string{"dried cranberries (small amount)", "strawberry"},
	},
	"pear juice": {
		Level:         "high",
		Groups:        []string{"excess fructose", "sorbitol"},
		Notes:         "Concentrated from pears; very high FODMAP",
		Substitutions: []string{"water (with lemon)", "cranberry juice (small amount)"},
	},
	"apple juice": {
		Level:         "high",
		Groups:        []string{"excess fructose", "sorbitol"},
		Notes:         "Concentrated from apples; very high FODMAP",
		Substitutions: []string{"water (with lemon)", "cranberry juice (small amount)"},
	},

	// Polyols (mannitol)
	"mushroom": {
		Level:         "high",
		Groups:        []string{"mannitol"},
		Notes:         "High in mannitol; common varieties (button, portobello, shiitake) are all high",
		Substitutions: []string{"dried porcini (small amount for flavor)", "eggplant", "zucchini"},
	},
	"cauliflower": {
		Level:         "high",
		Groups:        []string{"mannitol", "fructans"},
		Notes:         "High in both mannitol and fructans at standard servings; 1/2 cup may be tolerated",
		Substitutions: []string{"broccoli (small amount)", "zucchini", "bok choy"},
	},
	"celery": {
		Level:         "high",
		Groups:        []string{"mannitol"},
		Notes:         "High at standard servings; 1 small stalk (5cm) may be tolerated",
		Substitutions: []string{"celery (very small amount)", "carrot", "bok choy stem"},
	},
	"sweet potato": {
		Level:         "high",
		Groups:        []string{"mannitol"},
		Notes:         "High in mannitol at standard servings; 1/2 cup may be tolerated",
		Substitutions: []string{"potato", "butternut squash (small amount)", "carrot"},
	},

	// ---- MODERATE FODMAP ----

	"peas": {
		Level:         "moderate",
		Groups:        []string{"GOS", "mannitol"},
		Notes:         "Green peas; 1/3 cup is moderate; larger servings are high",
		Substitutions: []string{"green beans", "bok choy", "carrot"},
	},
	"coconut cream": {
		Level:         "moderate",
		Groups:        []string{"sorbitol"},
		Notes:         "Coconut milk (1/2 cup canned) is low FODMAP; coconut cream is more concentrated",
		Substitutions: []string{"coconut milk (canned, 1/2 cup)", "lactose-free cream"},
	},
	"leek green": {
		Level:         "moderate",
		Groups:        []string{"fructans"},
		Notes:         "The dark green leafy part only; the bulb is high FODMAP; 1/2 cup is moderate",
		Substitutions: []string{"spring onion greens", "chives"},
	},
	"brussels sprouts": {
		Level:         "moderate",
		Groups:        []string{"fructans"},
		Notes:         "Moderate at 2 sprouts; larger servings are high",
		Substitutions: []string{"bok choy", "spinach", "green beans"},
	},
	"edamame": {
		Level:         "moderate",
		Groups:        []string{"GOS"},
		Notes:         "1/2 cup edamame beans (without pods) is moderate; larger servings are high",
		Substitutions: []string{"firm tofu", "tempeh", "green beans"},
	},
	"butternut squash": {
		Level:         "moderate",
		Groups:        []string{"GOS", "fructans"},
		Notes:         "1/3 cup is moderate; larger servings are high",
		Substitutions: []string{"carrot", "pumpkin (small amount)", "sweet potato (small amount)"},
	},
	"brie": {
		Level:         "moderate",
		Groups:        []string{"lactose"},
		Notes:         "Moderate lactose; 2 slices may be tolerated",
		Substitutions: []string{"hard cheese", "camembert (small amount)", "lactose-free cheese"},
	},
	"camembert": {
		Level:         "moderate",
		Groups:        []string{"lactose"},
		Notes:         "Moderate lactose; small servings may be tolerated",
		Substitutions: []string{"hard cheese", "lactose-free cheese"},
	},
	"feta": {
		Level:         "moderate",
		Groups:        []string{"lactose"},
		Notes:         "Moderate lactose; 2 slices may be tolerated",
		Substitutions: []string{"hard cheese", "lactose-free feta-style cheese"},
	},
	"half-and-half": {
		Level:         "moderate",
		Groups:        []string{"lactose"},
		Notes:         "Moderate at small servings; 2 tbsp may be tolerated",
		Substitutions: []string{"lactose-free half-and-half", "coconut cream (small amount)"},
	},
	"blueberry": {
		Level:         "moderate",
		Groups:        []string{"excess fructose"},
		Notes:         "Moderate at 1/4 cup; larger servings are high; 1 cup is high FODMAP",
		Substitutions: []string{"strawberry", "raspberry", "kiwi"},
	},
	"grapefruit": {
		Level:         "moderate",
		Groups:        []string{"excess fructose"},
		Notes:         "Moderate at 1/2 grapefruit; larger servings are high",
		Substitutions: []string{"orange", "lemon", "lime"},
	},
	"coconut water": {
		Level:         "moderate",
		Groups:        []string{"sorbitol", "excess fructose"},
		Notes:         "Moderate at 1 cup; larger servings are high",
		Substitutions: []string{"water (with lemon or lime)", "herbal tea"},
	},
	"broccoli": {
		Level:         "moderate",
		Groups:        []string{"fructans", "GOS"},
		Notes:         "1/2 cup broccoli heads is moderate; stalks are higher in fructans",
		Substitutions: []string{"bok choy", "green beans", "zucchini"},
	},
	"cashew butter": {
		Level:         "moderate",
		Groups:        []string{"GOS"},
		Notes:         "1 tbsp is moderate; larger servings are high",
		Substitutions: []string{"peanut butter", "almond butter", "sunflower seed butter"},
	},
	"pistachio butter": {
		Level:         "moderate",
		Groups:        []string{"GOS"},
		Notes:         "1 tbsp is moderate; larger servings are high",
		Substitutions: []string{"peanut butter", "almond butter", "sunflower seed butter"},
	},

	// ---- LOW FODMAP ----

	// Proteins
	"chicken": {Level: "low", Groups: []string{}},
	"beef":    {Level: "low", Groups: []string{}},
	"pork":    {Level: "low", Groups: []string{}},
	"lamb":    {Level: "low", Groups: []string{}},
	"fish":    {Level: "low", Groups: []string{}},
	"shrimp":  {Level: "low", Groups: []string{}},
	"egg":     {Level: "low", Groups: []string{}},
	"tofu": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Firm tofu is low FODMAP; silken tofu is high in GOS",
	},
	"tempeh": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Fermentation reduces GOS content; generally well tolerated",
	},

	// Grains and starches
	"rice": {
		Level:  "low",
		Groups: []string{},
		Notes:  "White and brown rice are both low FODMAP",
	},
	"oats": {
		Level:  "low",
		Groups: []string{},
		Notes:  "1/2 cup rolled oats is low FODMAP; larger servings may contain moderate fructans",
	},
	"quinoa": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Low FODMAP at 1 cup cooked",
	},
	"corn": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Low FODMAP at 1/2 cob; canned corn is also low at 1/2 cup",
	},
	"rice noodles": {Level: "low", Groups: []string{}},
	"sourdough bread": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Long-fermented sourdough (24+ hours) reduces fructans; 2 slices is low FODMAP",
	},
	"gluten-free bread": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Most gluten-free breads are low FODMAP; check for high-FODMAP additives like inulin",
	},
	"tapioca": {Level: "low", Groups: []string{}},
	"millet":  {Level: "low", Groups: []string{}},
	"potato": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Low FODMAP at 1 medium potato; larger servings may be moderate",
	},
	"polenta":       {Level: "low", Groups: []string{}},
	"rice crackers": {Level: "low", Groups: []string{}},

	// Vegetables
	"tomato": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Up to 3 cherry tomatoes or 1/2 common tomato is low; larger amounts may be moderate",
	},
	"zucchini": {Level: "low", Groups: []string{}},
	"carrot":   {Level: "low", Groups: []string{}},
	"cucumber": {Level: "low", Groups: []string{}},
	"spinach":  {Level: "low", Groups: []string{}},
	"bell pepper": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Red and yellow bell peppers are low FODMAP; green bell pepper may be moderate in larger amounts",
	},
	"eggplant":    {Level: "low", Groups: []string{}},
	"green beans": {Level: "low", Groups: []string{}},
	"lettuce":     {Level: "low", Groups: []string{}},
	"bok choy":    {Level: "low", Groups: []string{}},
	"kale": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Low FODMAP at 1 cup; larger servings may be moderate in fructans",
	},
	"parsnip": {Level: "low", Groups: []string{}},
	"turnip":  {Level: "low", Groups: []string{}},
	"radish":  {Level: "low", Groups: []string{}},
	"olives":  {Level: "low", Groups: []string{}},
	"ginger":  {Level: "low", Groups: []string{}, Notes: "Fresh ginger is low FODMAP; commonly used in cooking"},
	"chili pepper": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Fresh chili is low FODMAP in small amounts; check for onion/garlic in chili powders",
	},
	"scallion green": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Green part only; the white/bulb part is high FODMAP",
	},
	"chives":   {Level: "low", Groups: []string{}},
	"basil":    {Level: "low", Groups: []string{}},
	"parsley":  {Level: "low", Groups: []string{}},
	"cilantro": {Level: "low", Groups: []string{}},
	"mint":     {Level: "low", Groups: []string{}},
	"rosemary": {Level: "low", Groups: []string{}},
	"thyme":    {Level: "low", Groups: []string{}},
	"oregano":  {Level: "low", Groups: []string{}},
	"cumin":    {Level: "low", Groups: []string{}},
	"turmeric": {Level: "low", Groups: []string{}},
	"paprika":  {Level: "low", Groups: []string{}},
	"cinnamon": {Level: "low", Groups: []string{}},
	"nutmeg":   {Level: "low", Groups: []string{}},

	// Fruits
	"banana": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Firm/slightly green banana is low FODMAP; very ripe banana develops excess fructose",
	},
	"strawberry": {Level: "low", Groups: []string{}},
	"orange":     {Level: "low", Groups: []string{}},
	"mandarin": {
		Level:  "low",
		Groups: []string{},
		Notes:  "1 small mandarin is low; larger servings may be moderate in excess fructose",
	},
	"kiwi":       {Level: "low", Groups: []string{}},
	"lemon":      {Level: "low", Groups: []string{}},
	"lime":       {Level: "low", Groups: []string{}},
	"grape":      {Level: "low", Groups: []string{}},
	"cantaloupe": {Level: "low", Groups: []string{}},
	"honeydew": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Low FODMAP at 1 cup cubed; larger servings may be moderate",
	},
	"papaya":        {Level: "low", Groups: []string{}},
	"passion fruit": {Level: "low", Groups: []string{}},
	"raspberry": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Low FODMAP at 1 cup; very high in fiber",
	},
	"rhubarb": {Level: "low", Groups: []string{}},
	"pineapple": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Low FODMAP at 1 cup; larger servings may be moderate in excess fructose",
	},
	"starfruit": {Level: "low", Groups: []string{}},
	"cranberry": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Low FODMAP at 1/4 cup; dried cranberries are also low at small servings",
	},
	"dragon fruit": {Level: "low", Groups: []string{}},

	// Dairy alternatives and low-lactose dairy
	"hard cheese": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Lactose is negligible in aged hard cheeses (cheddar, parmesan, swiss, gouda)",
	},
	"lactose-free milk": {Level: "low", Groups: []string{}},
	"almond milk":       {Level: "low", Groups: []string{}},
	"rice milk":         {Level: "low", Groups: []string{}},
	"coconut milk": {
		Level:  "low",
		Groups: []string{},
		Notes:  "1/2 cup canned coconut milk is low FODMAP; coconut cream is moderate",
	},
	"butter": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Very low lactose; well tolerated by most with lactose intolerance",
	},
	"ghee": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Clarified butter with virtually no lactose",
	},
	"parmesan": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Very low lactose due to long aging process",
	},
	"swiss cheese": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Low lactose due to aging; 2 slices is low FODMAP",
	},
	"mozzarella": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Fresh mozzarella is low FODMAP at 1/2 cup; contains small amount of lactose",
	},

	// Oils, fats, and sweeteners
	"olive oil": {Level: "low", Groups: []string{}},
	"garlic-infused oil": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Fructans are not oil-soluble, so garlic-infused oil is low FODMAP; commercial preparations are safe",
	},
	"coconut oil": {Level: "low", Groups: []string{}},
	"soy sauce": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Regular soy sauce is low FODMAP despite being made from wheat; fermentation removes fructans; tamari is also low",
	},
	"maple syrup": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Pure maple syrup is low FODMAP; avoid maple-flavored syrups with added high-FODMAP ingredients",
	},
	"table sugar":     {Level: "low", Groups: []string{}},
	"stevia":          {Level: "low", Groups: []string{}},
	"rice malt syrup": {Level: "low", Groups: []string{}},
	"golden syrup":    {Level: "low", Groups: []string{}},
	"vinegar": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Most vinegars (white, balsamic, apple cider, rice) are low FODMAP in standard servings",
	},
	"mustard": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Plain mustard is low FODMAP; check for onion/garlic in flavored mustards",
	},
	"mayonnaise": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Standard mayonnaise is low FODMAP; check flavored varieties for high-FODMAP ingredients",
	},

	// Nuts and seeds (low FODMAP at standard servings)
	"walnuts": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Low FODMAP at 10 halves; larger servings may be moderate",
	},
	"peanuts": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Low FODMAP at 1/4 cup; larger servings may be moderate",
	},
	"macadamia nuts": {Level: "low", Groups: []string{}},
	"pecans":         {Level: "low", Groups: []string{}},
	"almonds": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Low FODMAP at 10 almonds; larger servings may be moderate",
	},
	"pine nuts": {Level: "low", Groups: []string{}},
	"sesame seeds": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Low FODMAP at 1 tbsp; tahini is also low at 2 tbsp",
	},
	"sunflower seeds": {Level: "low", Groups: []string{}},
	"pumpkin seeds":   {Level: "low", Groups: []string{}},
	"chia seeds": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Low FODMAP at 2 tbsp; larger servings may be moderate in fructans",
	},
	"flaxseed": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Low FODMAP at 1 tbsp; larger servings may be moderate",
	},
	"tahini": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Low FODMAP at 2 tbsp; sesame paste",
	},
	"peanut butter": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Low FODMAP at 2 tbsp; check for added high-FODMAP ingredients",
	},
	"almond butter": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Low FODMAP at 1 tbsp; larger servings may be moderate",
	},

	// Beverages
	"coffee": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Regular and decaf coffee are low FODMAP at standard servings; avoid high-FODMAP add-ins",
	},
	"tea": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Black, green, and most herbal teas are low FODMAP; avoid chamomile and fennel tea",
	},
	"water": {Level: "low", Groups: []string{}},
	"wine": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Low FODMAP at 1 glass; sweet wines may be higher in fructose",
	},
	"beer": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Low FODMAP at 1 standard drink; some craft beers may be higher in fructans",
	},
	"spirits": {
		Level:  "low",
		Groups: []string{},
		Notes:  "Vodka, gin, rum, and whiskey are low FODMAP; avoid mixers with high-FODMAP ingredients",
	},
}
