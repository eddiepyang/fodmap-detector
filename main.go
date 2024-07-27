package main

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
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
	return Review{Text: "pizza was ok"}
}

func main() {
	ctx := context.Background()

	_, err := flags.Parse(&Opts)
	if err != nil {
		log.Fatal("error loading flag")
	}

	// Access your API key as an environment variable (see "Set up your API key" above)
	client, err := genai.NewClient(ctx, option.WithAPIKey(os.Getenv("GEMINI_KEY")))
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	if Opts.Model != "" {
		model = Opts.Model
	}
	fmt.Printf("running with model %v \n\n", model)

	tmpl, err := template.New("review").Parse(prompt)
	if err != nil {
		panic(err)
	}
	buffer := new(bytes.Buffer)
	err = tmpl.Execute(buffer, getReview())
	if err != nil {
		panic(err)
	}
	fmt.Println(buffer)

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
		Parts: []genai.Part{genai.Text("You are a professional food critic who is very good at determining ingredients")},
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

func printResponse(resp *genai.GenerateContentResponse) {
	for _, cand := range resp.Candidates {
		if cand.Content != nil {
			for _, part := range cand.Content.Parts {
				fmt.Print(part)
			}
		}
	}
	fmt.Println("---")
}
