package main

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"os"

	"github.com/google/generative-ai-go/genai"
	"github.com/jessevdk/go-flags"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

//go:embed prompt.txt
var prompt string

var model string = "gemini-1.5-flash"

// var opts struct {

// 	// Example of a callback, called each time the option is found.
// 	Model string `short:"m" long:"model" description:"model to use"`
// }

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
	llm := client.GenerativeModel(model)

	iter := llm.GenerateContentStream(ctx, genai.Text(prompt))
	if err != nil {
		log.Fatal(err)
	}
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
