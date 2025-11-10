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
	archive      = "../../data/yelp_dataset.tar" //todo: move this to config
	reivewFile   = "yelp_academic_dataset_review.json"
	WriteStopRow = 3
)
