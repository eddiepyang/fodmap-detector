package main

import (
	"bytes"
	"context"
	_ "embed"
	"log"
	"os"
	"text/template"

	"github.com/google/generative-ai-go/genai"
	"github.com/jessevdk/go-flags"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

//go:embed prompt.txt
var prompt string

var model string = "gemini-1.5-flash"

type Review struct {
	Business_id string
	Reviewer_id string
	Text        string
}

func getReview() Review {
	return Review{Text: `Don’t expect fireworks from Le Rock’s shellfish program: 
	Razor clams arrive in their shells, predictably chopped up and garnished with a touch of lime. 
	Raw scallops taste like nothing. In the mood for a maritime sugar high without much complexity? 
	Capers and eggs (marginally) boost the flavor of one-note Dungeness crab ($68). 
	Things get more interesting with the otherwise ordinary shrimp cocktail. 
	The kitchen serves the crustaceans as they are, plump and sweet, packing a touch of court-bouillon aroma. 
	But right after you gobble them, a waiter magically arrives with something that too many brasseries like to discard: 
	the heads. They’re fried to a golden hue, packing a satisfying crunch and a whisper of funk.`}
}

func printResponse(resp *genai.GenerateContentResponse) {
	for _, cand := range resp.Candidates {
		if cand.Content != nil {
			for _, part := range cand.Content.Parts {
				log.Print(part)
			}
		}
	}
	log.Println("---")
}

func main() {
	ctx := context.Background()

	_, err := flags.Parse(&Opts)
	if err != nil {
		log.Fatal("error loading flag")
	}

	//todo: wrap this in function and add goroutines
	client, err := genai.NewClient(ctx, option.WithAPIKey(os.Getenv("GEMINI_KEY")))
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	if Opts.Model != "" {
		model = Opts.Model
	}
	log.Printf("running with model %v \n\n", model)

	tmpl, err := template.New("review").Parse(prompt)
	if err != nil {
		panic(err)
	}
	buffer := new(bytes.Buffer)
	err = tmpl.Execute(buffer, getReview())
	if err != nil {
		panic(err)
	}
	log.Println(buffer)

	llm := client.GenerativeModel(model)
	llm.SafetySettings = []*genai.SafetySetting{
		{
			Category:  genai.HarmCategoryHarassment,
			Threshold: genai.HarmBlockOnlyHigh,
		},
		{
			Category:  genai.HarmCategoryDangerousContent,
			Threshold: genai.HarmBlockOnlyHigh,
		},
		{
			Category:  genai.HarmCategoryHateSpeech,
			Threshold: genai.HarmBlockOnlyHigh,
		},
		{
			Category:  genai.HarmCategorySexuallyExplicit,
			Threshold: genai.HarmBlockOnlyHigh,
		},
	}
	llm.SystemInstruction = &genai.Content{
		Parts: []genai.Part{genai.Text(
			"You are a professional food critic and board certified nutritionist who is very good at determining ingredients")},
	}

	iter := llm.GenerateContentStream(ctx, genai.Text(buffer.String()))

	for {
		item, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Fatal(err)
		}
		// spew.Printf("responses are: %v, \n", item.Candidates)
		printResponse(item)
	}

}
