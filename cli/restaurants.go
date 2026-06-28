package cli

import (
	"fmt"
	"os"

	"fodmap/menusearch"
	"fodmap/server"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
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
	importCmd.Flags().Bool("skip-discovery", false, "Skip enqueueing discovery jobs")
	restaurantsCmd.AddCommand(importCmd)

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List restaurants from the database",
		RunE:  runListRestaurants,
	}
	listCmd.Flags().String("postgres-dsn", "", "PostgreSQL DSN")
	listCmd.Flags().String("status", "pending_discovery", "Filter by status")
	listCmd.Flags().Int("limit", 50, "Limit the number of records")
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

	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 10},
		},
	})
	if err != nil {
		return fmt.Errorf("create river client: %w", err)
	}

	fmt.Printf("Fetching restaurants for area %q...\n", areaName)
	reader, err := menusearch.FetchNYCRestaurants(ctx, areaName, appToken)
	if err != nil {
		return fmt.Errorf("fetch restaurants: %w", err)
	}
	defer func() { _ = reader.Close() }()

	records, err := menusearch.ParseNYCCSV(reader)
	if err != nil {
		return fmt.Errorf("parse csv: %w", err)
	}
	if len(records) > limit {
		records = records[:limit]
	}

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
			_, err = riverClient.Insert(ctx, menusearch.DiscoverMenuURLArgs{
				CAMIS:    rec.CAMIS,
				DBA:      rec.DBA,
				Building: rec.Building,
				Street:   rec.Street,
				Boro:     rec.Boro,
				Attempt:  1,
			}, nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to enqueue discovery for %s: %v\n", rec.CAMIS, err)
			}
		}
	}

	fmt.Printf("Imported %d restaurants.\n", len(records))
	return nil
}

func runListRestaurants(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	dsn, _ := cmd.Flags().GetString("postgres-dsn")
	status, _ := cmd.Flags().GetString("status")
	limit, _ := cmd.Flags().GetInt("limit")

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

	restaurants, err := store.List(ctx, status, "", limit, 0)
	if err != nil {
		return fmt.Errorf("list restaurants: %w", err)
	}

	fmt.Printf("Found %d restaurants with status %q:\n", len(restaurants), status)
	for _, r := range restaurants {
		menuURL := r.MenuURL
		if menuURL == nil {
			menuURL = new(string)
			*menuURL = ""
		}
		fmt.Printf("- %s: %s (Menu URL: %s)\n", r.CAMIS, r.DBA, *menuURL)
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
	if r.MenuURL == nil || *r.MenuURL == "" {
		return fmt.Errorf("restaurant %s has no menu URL", camis)
	}

	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 10},
		},
	})
	if err != nil {
		return fmt.Errorf("create river client: %w", err)
	}

	if err = store.UpdateScrapeResult(ctx, r.CAMIS, menusearch.StatusURLFound, 0, ""); err != nil {
		return fmt.Errorf("update status: %w", err)
	}

	_, err = riverClient.Insert(ctx, menusearch.ScrapeMenuArgs{
		CAMIS:            r.CAMIS,
		URL:              *r.MenuURL,
		DBA:              r.DBA,
		DiscoveryEventID: uuid.NewString(),
	}, nil)
	if err != nil {
		return fmt.Errorf("enqueue scrape: %w", err)
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

	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 10},
		},
	})
	if err != nil {
		return fmt.Errorf("create river client: %w", err)
	}

	_, err = riverClient.Insert(ctx, menusearch.DiscoverMenuURLArgs{
		CAMIS:    r.CAMIS,
		DBA:      r.DBA,
		Building: safeStr(r.Building),
		Street:   safeStr(r.Street),
		Boro:     safeStr(r.Boro),
		Attempt:  1,
	}, nil)
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

	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 10},
		},
	})
	if err != nil {
		return fmt.Errorf("create river client: %w", err)
	}

	if r.MenuURL != nil && *r.MenuURL != "" {
		_, err = riverClient.Insert(ctx, menusearch.ScrapeMenuArgs{
			CAMIS:            r.CAMIS,
			URL:              *r.MenuURL,
			DBA:              r.DBA,
			DiscoveryEventID: uuid.NewString(),
		}, nil)
		if err != nil {
			return fmt.Errorf("enqueue scrape: %w", err)
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
			Attempt:  1,
		}, nil)
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
