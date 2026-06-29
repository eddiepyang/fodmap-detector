package cli

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"fodmap/menusearch"
	"fodmap/pipeline"
	"fodmap/scraper"
	"fodmap/search"
	"fodmap/server"

	"github.com/google/uuid"
	"github.com/hamba/avro/v2/ocf"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var restaurantsCmd = &cobra.Command{
	Use:   "restaurants",
	Short: "Manage restaurant menu discovery and scraping",
}

func init() {
	rootCmd.AddCommand(restaurantsCmd)

	importCmd := &cobra.Command{
		Use:   "import",
		Short: "Import restaurants from NYC OpenData",
		RunE:  runImportRestaurants,
	}
	importCmd.Flags().String("area", "", "NTA or ZIP to import (e.g. astoria-lic)")
	importCmd.Flags().String("postgres-dsn", "", "PostgreSQL DSN")
	importCmd.Flags().String("nyc-app-token", "", "NYC OpenData App Token")
	importCmd.Flags().Int("limit", 100, "Limit the number of records to import")
	importCmd.Flags().Int("offset", 0, "Offset the number of records to import")
	importCmd.Flags().Bool("skip-discovery", false, "Skip enqueueing discovery jobs")
	importCmd.Flags().String("bronze-dir", "data/bronze/restaurants", "Directory to save raw restaurant downloads")
	restaurantsCmd.AddCommand(importCmd)

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List restaurants from the database",
		RunE:  runListRestaurants,
	}
	listCmd.Flags().String("postgres-dsn", "", "PostgreSQL DSN")
	listCmd.Flags().String("status", "pending_discovery", "Filter by status")
	listCmd.Flags().Int("limit", 50, "Limit the number of records")
	listCmd.Flags().Int("offset", 0, "Offset for the records")
	restaurantsCmd.AddCommand(listCmd)

	scrapeCmd := &cobra.Command{
		Use:   "scrape [camis]",
		Short: "Enqueue a scrape job for a specific CAMIS",
		Args:  cobra.ExactArgs(1),
		RunE:  runEnqueueScrape,
	}
	scrapeCmd.Flags().String("postgres-dsn", "", "PostgreSQL DSN")
	scrapeCmd.Flags().String("extractor-url", "http://localhost:8765", "URL for the OCR/PDF extractor service")
	restaurantsCmd.AddCommand(scrapeCmd)

	discoverCmd := &cobra.Command{
		Use:   "discover [camis]",
		Short: "Enqueue a discovery job for a specific CAMIS",
		Args:  cobra.ExactArgs(1),
		RunE:  runEnqueueDiscover,
	}
	discoverCmd.Flags().String("postgres-dsn", "", "PostgreSQL DSN")
	restaurantsCmd.AddCommand(discoverCmd)

	retryCmd := &cobra.Command{
		Use:   "retry [camis]",
		Short: "Reset a failed restaurant and re-queue it",
		Args:  cobra.ExactArgs(1),
		RunE:  runRetryRestaurant,
	}
	retryCmd.Flags().String("postgres-dsn", "", "PostgreSQL DSN")
	restaurantsCmd.AddCommand(retryCmd)

	replayMenusCmd := &cobra.Command{
		Use:   "replay-menus",
		Short: "Re-hydrate the menu index from Avro silver layer",
		RunE:  runReplayMenus,
	}
	replayMenusCmd.Flags().String("avro-dir", "data/silver/menus", "Directory containing .avro files")
	replayMenusCmd.Flags().String("store", "weaviate", "Storage backend (deprecated alias for --menu-store): weaviate | postgres | pinecone")
	replayMenusCmd.Flags().String("menu-store", "", "Menu store backend: postgres | weaviate | dual (preferred over --store)")
	replayMenusCmd.Flags().String("weaviate", "localhost:8090", "Weaviate host:port")
	replayMenusCmd.Flags().String("weaviate-scheme", "http", "Weaviate scheme (http or https)")
	replayMenusCmd.Flags().String("weaviate-api-key", "", "Weaviate API key")
	replayMenusCmd.Flags().String("postgres-dsn", "", "PostgreSQL DSN")
	replayMenusCmd.Flags().String("pinecone-api-key", "", "Pinecone API key")
	replayMenusCmd.Flags().String("pinecone-index-host", "", "Pinecone index host")
	replayMenusCmd.Flags().String("embed-backend", "ollama", "Embedding backend: ollama | vectorizer")
	replayMenusCmd.Flags().String("ollama-url", "http://localhost:11434", "Ollama base URL")
	replayMenusCmd.Flags().String("ollama-model", "nomic-embed-text", "Ollama embedding model")
	replayMenusCmd.Flags().String("vectorizer", "", "HTTP vectorizer host:port")
	restaurantsCmd.AddCommand(replayMenusCmd)
}

func runImportRestaurants(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	areaName, _ := cmd.Flags().GetString("area")
	dsn, _ := cmd.Flags().GetString("postgres-dsn")
	appToken, _ := cmd.Flags().GetString("nyc-app-token")
	limit, _ := cmd.Flags().GetInt("limit")
	skipDiscovery, _ := cmd.Flags().GetBool("skip-discovery")

	if dsn == "" {
		dsn = viper.GetString("POSTGRES_DSN")
	}
	if appToken == "" {
		appToken = viper.GetString("NYC_APP_TOKEN")
	}

	if areaName == "" {
		return fmt.Errorf("must specify --area")
	}
	if dsn == "" {
		return fmt.Errorf("must specify --postgres-dsn")
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect to db: %w", err)
	}
	defer pool.Close()

	store := menusearch.NewStore(pool)

	riverClient, err := newRiverClient(pool, &river.Config{})
	if err != nil {
		return fmt.Errorf("create river client: %w", err)
	}

	bronzeDir, _ := cmd.Flags().GetString("bronze-dir")
	todayStr := time.Now().UTC().Format("2006-01-02")
	avroPath := filepath.Join(bronzeDir, fmt.Sprintf("%s-%s.avro", areaName, todayStr))

	var records []menusearch.NYCRestaurantRecord

	if _, err := os.Stat(avroPath); err == nil {
		fmt.Printf("Reusing today's avro file: %s\n", avroPath)
		records, err = menusearch.ReadNYCRestaurantAvro(avroPath)
		if err != nil {
			return fmt.Errorf("read avro: %w", err)
		}
	} else {
		since, err := store.MaxUpdatedAt(ctx)
		if err != nil {
			fmt.Printf("Warning: failed to get max updated_at: %v\n", err)
		}

		fmt.Printf("Fetching restaurants for area %q (since %v)...\n", areaName, since)
		reader, err := menusearch.FetchNYCRestaurants(ctx, areaName, appToken, since)
		if err != nil {
			return fmt.Errorf("fetch restaurants: %w", err)
		}
		defer func() { _ = reader.Close() }()

		records, err = menusearch.ParseNYCCSV(reader)
		if err != nil {
			return fmt.Errorf("parse csv: %w", err)
		}

		if len(records) > 0 {
			fmt.Printf("Saving %d records to %s\n", len(records), avroPath)
			if err := menusearch.WriteNYCRestaurantAvro(ctx, avroPath, records); err != nil {
				return fmt.Errorf("write avro: %w", err)
			}
		}
	}

	offset, _ := cmd.Flags().GetInt("offset")
	records = paginateRecords(records, limit, offset)

	fmt.Printf("Fetched %d unique restaurants.\n", len(records))

	for _, rec := range records {
		err := store.Upsert(ctx, server.Restaurant{
			CAMIS:     rec.CAMIS,
			DBA:       rec.DBA,
			Boro:      strPtr(rec.Boro),
			Building:  strPtr(rec.Building),
			Street:    strPtr(rec.Street),
			Zipcode:   strPtr(rec.Zipcode),
			Phone:     strPtr(rec.Phone),
			Cuisine:   strPtr(rec.CuisineDescription),
			Latitude:  floatPtr(rec.Latitude),
			Longitude: floatPtr(rec.Longitude),
			NTA:       strPtr(rec.NTA),
			Status:    menusearch.StatusPendingDiscovery,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to upsert %s: %v\n", rec.CAMIS, err)
			continue
		}

		if !skipDiscovery {
			maxAttempts := viper.GetInt("scrape-max-attempts")
			if maxAttempts <= 0 {
				maxAttempts = 3
			}
			_, err = riverClient.Insert(ctx, menusearch.DiscoverMenuURLArgs{
				CAMIS:    rec.CAMIS,
				DBA:      rec.DBA,
				Building: rec.Building,
				Street:   rec.Street,
				Boro:     rec.Boro,
				Zipcode:  rec.Zipcode,
				Attempt:  1,
			}, &river.InsertOpts{MaxAttempts: maxAttempts})
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to enqueue discovery for %s: %v\n", rec.CAMIS, err)
			}
		}
	}

	fmt.Printf("Imported %d restaurants.\n", len(records))
	return nil
}

// paginateRecords applies limit and offset to a slice of restaurant records.
func paginateRecords(records []menusearch.NYCRestaurantRecord, limit, offset int) []menusearch.NYCRestaurantRecord {
	if offset > 0 {
		if offset >= len(records) {
			return nil
		}
		records = records[offset:]
	}

	if limit > 0 && len(records) > limit {
		records = records[:limit]
	}

	return records
}

func runListRestaurants(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	dsn, _ := cmd.Flags().GetString("postgres-dsn")
	status, _ := cmd.Flags().GetString("status")
	limit, _ := cmd.Flags().GetInt("limit")
	offset, _ := cmd.Flags().GetInt("offset")

	if dsn == "" {
		dsn = viper.GetString("POSTGRES_DSN")
	}
	if dsn == "" {
		return fmt.Errorf("must specify --postgres-dsn")
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect to db: %w", err)
	}
	defer pool.Close()

	store := menusearch.NewStore(pool)

	restaurants, err := store.List(ctx, status, "", limit, offset)
	if err != nil {
		return fmt.Errorf("list restaurants: %w", err)
	}

	fmt.Printf("Found %d restaurants with status %q:\n", len(restaurants), status)
	for _, r := range restaurants {
		websiteURL := ""
		if r.WebsiteURL != nil {
			websiteURL = *r.WebsiteURL
		}
		if len(r.MenuURLs) > 0 {
			websiteURL += fmt.Sprintf(" (+%d menus)", len(r.MenuURLs))
		}
		fmt.Printf("- %s: %s (Website: %s)\n", r.CAMIS, r.DBA, websiteURL)
	}
	return nil
}

func runEnqueueScrape(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	camis := args[0]
	dsn, _ := cmd.Flags().GetString("postgres-dsn")

	if dsn == "" {
		dsn = viper.GetString("POSTGRES_DSN")
	}
	if dsn == "" {
		return fmt.Errorf("must specify --postgres-dsn")
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect to db: %w", err)
	}
	defer pool.Close()

	store := menusearch.NewStore(pool)
	r, err := store.Get(ctx, camis)
	if err != nil {
		return fmt.Errorf("get restaurant: %w", err)
	}
	if r == nil {
		return fmt.Errorf("restaurant %s not found", camis)
	}
	if len(r.MenuURLs) == 0 {
		return fmt.Errorf("restaurant %s has no menu URLs", camis)
	}

	riverClient, err := newRiverClient(pool, &river.Config{})
	if err != nil {
		return fmt.Errorf("create river client: %w", err)
	}

	if err = store.UpdateScrapeResult(ctx, r.CAMIS, menusearch.StatusURLFound, 0, ""); err != nil {
		return fmt.Errorf("update status: %w", err)
	}

	eventID := uuid.NewString()
	maxAttempts := viper.GetInt("scrape-max-attempts")
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	for _, u := range r.MenuURLs {
		_, err = riverClient.Insert(ctx, menusearch.ScrapeMenuArgs{
			CAMIS:            r.CAMIS,
			URL:              u,
			DBA:              r.DBA,
			DiscoveryEventID: eventID,
		}, &river.InsertOpts{MaxAttempts: maxAttempts})
		if err != nil {
			return fmt.Errorf("enqueue river job for %s: %w", u, err)
		}
	}

	fmt.Printf("Enqueued scrape job for %s\n", camis)
	return nil
}

func runEnqueueDiscover(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	camis := args[0]
	dsn, _ := cmd.Flags().GetString("postgres-dsn")

	if dsn == "" {
		dsn = viper.GetString("POSTGRES_DSN")
	}
	if dsn == "" {
		return fmt.Errorf("must specify --postgres-dsn")
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect to db: %w", err)
	}
	defer pool.Close()

	store := menusearch.NewStore(pool)
	r, err := store.Get(ctx, camis)
	if err != nil {
		return fmt.Errorf("get restaurant: %w", err)
	}
	if r == nil {
		return fmt.Errorf("restaurant %s not found", camis)
	}

	riverClient, err := newRiverClient(pool, &river.Config{})
	if err != nil {
		return fmt.Errorf("create river client: %w", err)
	}

	maxAttempts := viper.GetInt("scrape-max-attempts")
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	_, err = riverClient.Insert(ctx, menusearch.DiscoverMenuURLArgs{
		CAMIS:    r.CAMIS,
		DBA:      r.DBA,
		Building: safeStr(r.Building),
		Street:   safeStr(r.Street),
		Boro:     safeStr(r.Boro),
		Zipcode:  safeStr(r.Zipcode),
		Attempt:  1,
	}, &river.InsertOpts{MaxAttempts: maxAttempts})
	if err != nil {
		return fmt.Errorf("enqueue discover: %w", err)
	}

	err = store.UpdateScrapeResult(ctx, r.CAMIS, menusearch.StatusPendingDiscovery, 0, "")
	if err != nil {
		return fmt.Errorf("update status: %w", err)
	}

	fmt.Printf("Enqueued discovery job for %s\n", camis)
	return nil
}

func safeStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func floatPtr(f float64) *float64 {
	if f == 0 {
		return nil
	}
	return &f
}

func runRetryRestaurant(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	camis := args[0]
	dsn, _ := cmd.Flags().GetString("postgres-dsn")

	if dsn == "" {
		dsn = viper.GetString("POSTGRES_DSN")
	}
	if dsn == "" {
		return fmt.Errorf("must specify --postgres-dsn")
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect to db: %w", err)
	}
	defer pool.Close()

	store := menusearch.NewStore(pool)
	r, err := store.Get(ctx, camis)
	if err != nil {
		return fmt.Errorf("get restaurant: %w", err)
	}
	if r == nil {
		return fmt.Errorf("restaurant %s not found", camis)
	}

	riverClient, err := newRiverClient(pool, &river.Config{})
	if err != nil {
		return fmt.Errorf("create river client: %w", err)
	}

	maxAttempts := viper.GetInt("scrape-max-attempts")
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	if len(r.MenuURLs) > 0 {
		eventID := uuid.NewString()
		for _, u := range r.MenuURLs {
			_, err = riverClient.Insert(ctx, menusearch.ScrapeMenuArgs{
				CAMIS:            r.CAMIS,
				URL:              u,
				DBA:              r.DBA,
				DiscoveryEventID: eventID,
			}, &river.InsertOpts{MaxAttempts: maxAttempts})
			if err != nil {
				return fmt.Errorf("enqueue scrape %s: %w", u, err)
			}
		}
		err = store.UpdateScrapeResult(ctx, r.CAMIS, menusearch.StatusURLFound, 0, "")
		if err != nil {
			return fmt.Errorf("update status: %w", err)
		}
		fmt.Printf("Re-enqueued scrape job for %s\n", camis)
	} else {
		_, err = riverClient.Insert(ctx, menusearch.DiscoverMenuURLArgs{
			CAMIS:    r.CAMIS,
			DBA:      r.DBA,
			Building: safeStr(r.Building),
			Street:   safeStr(r.Street),
			Boro:     safeStr(r.Boro),
			Zipcode:  safeStr(r.Zipcode),
			Attempt:  1,
		}, &river.InsertOpts{MaxAttempts: maxAttempts})
		if err != nil {
			return fmt.Errorf("enqueue discover: %w", err)
		}
		err = store.UpdateScrapeResult(ctx, r.CAMIS, menusearch.StatusPendingDiscovery, 0, "")
		if err != nil {
			return fmt.Errorf("update status: %w", err)
		}
		fmt.Printf("Re-enqueued discovery job for %s\n", camis)
	}

	return nil
}

func runReplayMenus(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	avroDir, _ := cmd.Flags().GetString("avro-dir")
	storeType, _ := cmd.Flags().GetString("store")

	weaviateHost, _ := cmd.Flags().GetString("weaviate")
	weaviateScheme, _ := cmd.Flags().GetString("weaviate-scheme")
	weaviateAPIKey, _ := cmd.Flags().GetString("weaviate-api-key")
	dsn, _ := cmd.Flags().GetString("postgres-dsn")
	_, _ = cmd.Flags().GetString("pinecone-api-key")
	_, _ = cmd.Flags().GetString("pinecone-index-host")

	embedBackend, _ := cmd.Flags().GetString("embed-backend")
	ollamaURL, _ := cmd.Flags().GetString("ollama-url")
	ollamaModel, _ := cmd.Flags().GetString("ollama-model")
	vectorizerHost, _ := cmd.Flags().GetString("vectorizer")

	if dsn == "" {
		dsn = viper.GetString("POSTGRES_DSN")
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect to db: %w", err)
	}
	defer pool.Close()
	restaurantStore := menusearch.NewStore(pool)

	var embedder search.Embedder
	if embedBackend == "vectorizer" {
		embedder = search.NewVectorizerClient(vectorizerHost)
	} else {
		embedder = search.NewOllamaEmbedder(ollamaURL, ollamaModel)
	}
	defer func() { _ = embedder.Close() }()

	// Menu store selection. --menu-store is preferred; --store is the legacy
	// alias (weaviate|postgres|pinecone) preserved for backward compat.
	menuStoreType, _ := cmd.Flags().GetString("menu-store")
	if menuStoreType == "" {
		menuStoreType = storeType
		if menuStoreType == "pinecone" {
			return fmt.Errorf("pinecone does not support menus yet; use --menu-store=postgres|weaviate|dual")
		}
	}
	menuStore, err := server.NewMenuStore(ctx, server.MenuStoreConfig{
		Type:           menuStoreType,
		PostgresDSN:    dsn,
		WeaviateHost:   weaviateHost,
		WeaviateScheme: weaviateScheme,
		WeaviateAPIKey: weaviateAPIKey,
		Embedder:       embedder,
	})
	if err != nil {
		return fmt.Errorf("building menu store: %w", err)
	}

	var files []string
	if err := filepath.WalkDir(avroDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Ext(path) == ".avro" {
			files = append(files, path)
		}
		return nil
	}); err != nil {
		return err
	}

	fmt.Printf("Found %d avro files to replay\n", len(files))

	for _, file := range files {
		f, err := os.Open(file)
		if err != nil {
			slog.Error("failed to open file", "path", file, "error", err)
			continue
		}
		decoder, err := ocf.NewDecoder(f)
		if err != nil {
			slog.Error("failed to create decoder", "path", file, "error", err)
			_ = f.Close()
			continue
		}

		for decoder.HasNext() {
			var record map[string]any
			if err := decoder.Decode(&record); err != nil {
				slog.Error("failed to decode avro record", "path", file, "error", err)
				break
			}

			camis, _ := record["camis"].(string)
			sourceURL, _ := record["source_url"].(string)
			restaurantName, _ := record["restaurant_name"].(string)

			itemsAny, ok := record["items"].([]any)
			if !ok {
				continue
			}

			res := scraper.MenuExtractionResult{
				RestaurantName: restaurantName,
				SourceURL:      sourceURL,
				Items:          make([]scraper.MenuEntry, 0, len(itemsAny)),
			}

			rest, err := restaurantStore.Get(ctx, camis)
			if err != nil {
				slog.Warn("failed to get restaurant for address enrichment", "camis", camis, "error", err)
			} else if rest != nil {
				if rest.Boro != nil {
					res.City = *rest.Boro
				}
				res.State = "NY"
				if rest.Address != nil {
					res.Address = *rest.Address
				}
				if rest.Phone != nil {
					res.PhoneNumber = *rest.Phone
				}
			}

			for _, itemA := range itemsAny {
				itemMap, ok := itemA.(map[string]any)
				if !ok {
					continue
				}
				dishName, _ := itemMap["dish_name"].(string)
				description, _ := itemMap["description"].(string)
				hasFull, _ := itemMap["has_full_ingredients"].(bool)

				entry := scraper.MenuEntry{
					DishName:           dishName,
					Description:        description,
					HasFullIngredients: hasFull,
				}
				if si, ok := itemMap["stated_ingredients"].([]any); ok {
					for _, s := range si {
						if str, ok := s.(string); ok {
							entry.StatedIngredients = append(entry.StatedIngredients, str)
						}
					}
				}
				res.Items = append(res.Items, entry)
			}

			count, err := pipeline.StoreMenu(ctx, &res, sourceURL, menuStore, embedder)
			if err != nil {
				slog.Error("failed to store menu items", "camis", camis, "error", err)
			} else {
				fmt.Printf("stored %d items for %s\n", count, camis)
			}
		}
		_ = f.Close()
	}

	return nil
}
