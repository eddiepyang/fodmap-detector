package data

const (
	YelpSchema = `{
	"Tag":"name=yelp-reviews",
	"Fields":[
		{"Tag":"name=review_id, inname=review_id, type=BYTE_ARRAY, repetitiontype=REQUIRED"},
		{"Tag":"name=user_id, inname=user_id, type=BYTE_ARRAY, repetitiontype=REQUIRED"},
		{"Tag":"name=business_id, inname=business_id, type=BYTE_ARRAY, repetitiontype=REQUIRED"},
		{"Tag":"name=stars, inname=stars, type=FLOAT, repetitiontype=REQUIRED"},
		{"Tag":"name=useful, inname=useful, type=FLOAT, repetitiontype=REQUIRED"},
		{"Tag":"name=cool, inname=cool, type=FLOAT, repetitiontype=REQUIRED"},
		{"Tag":"name=funny, inname=funny, type=FLOAT, repetitiontype=REQUIRED"},
		{"Tag":"name=text, inname=text, type=BYTE_ARRAY, repetitiontype=REQUIRED"}
    ]
}`
)

// ValidFodmapLevels is the set of allowed FODMAP classification levels.
// It is kept in lowercase to match the data layer.
var ValidFodmapLevels = []string{"high", "moderate", "low"}

// ValidFodmapGroups is the set of allowed FODMAP groups. Ingredients may
// belong to one or more of these groups.
var ValidFodmapGroups = []string{
	"fructans",
	"GOS",
	"lactose",
	"excess fructose",
	"sorbitol",
	"mannitol",
}
