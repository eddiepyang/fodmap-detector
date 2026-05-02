package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"fodmap/scraper"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var scrapeCmd = &cobra.Command{
	Use:   "scrape",
	Short: "Scrape a restaurant's menu and optionally run FODMAP analysis.",
	Example: `  # Scrape by direct URL
  fodmap-detector scrape --url "https://example-restaurant.com"

  # Scrape by restaurant name (uses OpenStreetMap to find the website)
  fodmap-detector scrape --name "Joe's Pizza" --area "New York City"

  # Scrape and run FODMAP analysis
  fodmap-detector scrape --url "https://example.com/menu" --analyze

  # Seed the NYC restaurant cache from OpenStreetMap (run once)
  fodmap-detector scrape --discover-nyc`,
	RunE: func(cmd *cobra.Command, args []string) error {
		url, _ := cmd.Flags().GetString("url")
		name, _ := cmd.Flags().GetString("name")
		area, _ := cmd.Flags().GetString("area")
		analyze, _ := cmd.Flags().GetBool("analyze")
		discoverNYC, _ := cmd.Flags().GetBool("discover-nyc")
		outputJSON, _ := cmd.Flags().GetBool("json")

		ollamaURL := viper.GetString("ollama-url")
		ollamaModel := viper.GetString("ollama-vision-model")
		dbPath := viper.GetString("scraper-db")

		cfg := scraper.Config{
			OllamaURL:     ollamaURL,
			VisionModel:   ollamaModel,
			ScraperDBPath: dbPath,
			DefaultArea:   area,
		}

		agent, err := scraper.NewAgent(cfg)
		if err != nil {
			return fmt.Errorf("initializing scraper: %w", err)
		}
		defer func() { _ = agent.Close() }()

		ctx := context.Background()

		// ---- Discover / seed mode ----
		if discoverNYC {
			fmt.Fprintf(os.Stderr, "Seeding NYC restaurant cache from OpenStreetMap...\n")
			count, err := agent.DiscoverNYC(ctx)
			if err != nil {
				return fmt.Errorf("discover NYC: %w", err)
			}
			fmt.Printf("Cached %d restaurants from OpenStreetMap.\n", count)
			return nil
		}

		// ---- Scrape mode ----
		if url == "" && name == "" {
			return fmt.Errorf("provide --url or --name")
		}

		req := scraper.ScrapeRequest{
			URL:          url,
			BusinessName: name,
			City:         area,
			Analyze:      analyze,
		}

		fmt.Fprintf(os.Stderr, "Scraping menu...\n")
		result, err := agent.Scrape(ctx, req)
		if err != nil {
			return fmt.Errorf("scrape failed: %w", err)
		}

		if outputJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(result)
		}

		// Human-readable output.
		fmt.Printf("\n🍽  %s\n", result.BusinessName)
		fmt.Printf("   Menu URL: %s\n", result.MenuURL)
		fmt.Printf("   Source:   %s\n", result.Source)
		fmt.Printf("   Items:    %d\n\n", len(result.Items))

		for _, item := range result.Items {
			line := "  • " + item.Name
			if item.Price != "" {
				line += "  " + item.Price
			}
			if item.Category != "" {
				line += "  [" + item.Category + "]"
			}
			fmt.Println(line)
			if item.Description != "" {
				fmt.Printf("    %s\n", item.Description)
			}
		}

		if result.Analysis != nil {
			fmt.Printf("\n📊 FODMAP Analysis\n")
			fmt.Printf("   %s\n", result.Analysis.Summary)
		}

		if len(result.Warnings) > 0 {
			fmt.Printf("\n⚠️  Warnings:\n")
			for _, w := range result.Warnings {
				fmt.Printf("   - %s\n", w)
			}
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(scrapeCmd)

	scrapeCmd.Flags().String("url", "", "Direct URL to a restaurant homepage or menu page")
	scrapeCmd.Flags().String("name", "", "Restaurant name (resolved via OpenStreetMap)")
	scrapeCmd.Flags().String("area", "New York City", "City/area name for OpenStreetMap lookup")
	scrapeCmd.Flags().Bool("analyze", false, "Run FODMAP analysis on extracted menu items")
	scrapeCmd.Flags().Bool("discover-nyc", false, "Seed the local restaurant cache from OpenStreetMap (run once)")
	scrapeCmd.Flags().Bool("json", false, "Output results as JSON")

	// Bind to service.yaml / env vars.
	_ = viper.BindPFlag("scraper-url", scrapeCmd.Flags().Lookup("url"))
}
