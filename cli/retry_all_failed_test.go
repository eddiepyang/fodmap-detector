package cli

import (
	"context"
	"os"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// newRetryAllFailedTestCmd builds a command with the same flags init()
// registers on retryAllFailedCmd, so runRetryAllFailed can be exercised in
// isolation without mutating global command state.
func newRetryAllFailedTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "retry-all-failed"}
	cmd.SetContext(context.Background())
	cmd.Flags().String("postgres-dsn", "", "PostgreSQL DSN")
	cmd.Flags().Int("limit", 0, "Max number of restaurants to retry (0 = all)")
	cmd.Flags().Int("batch-size", 500, "Page size for fetching failed restaurants from the DB")
	cmd.Flags().Bool("dry-run", false, "List the restaurants that would be retried without enqueuing")
	return cmd
}

func TestRunRetryAllFailed_MissingDSN(t *testing.T) {
	// Ensure no DSN is set from env or viper.
	viper.Set("POSTGRES_DSN", "")
	t.Cleanup(func() { viper.Set("POSTGRES_DSN", "") })
	if os.Getenv("POSTGRES_DSN") != "" {
		t.Skip("POSTGRES_DSN is set; skipping the missing-DSN test")
	}

	cmd := newRetryAllFailedTestCmd()
	if err := cmd.Flags().Set("postgres-dsn", ""); err != nil {
		t.Fatalf("setting flag: %v", err)
	}

	err := runRetryAllFailed(cmd, nil)
	if err == nil {
		t.Fatal("expected error for missing --postgres-dsn")
	}
}

func TestRunRetryAllFailed_BadBatchSize(t *testing.T) {
	cmd := newRetryAllFailedTestCmd()
	if err := cmd.Flags().Set("postgres-dsn", "postgres://u:p@127.0.0.1:1/none"); err != nil {
		t.Fatalf("setting flag: %v", err)
	}
	if err := cmd.Flags().Set("batch-size", "0"); err != nil {
		t.Fatalf("setting flag: %v", err)
	}

	err := runRetryAllFailed(cmd, nil)
	if err == nil {
		t.Fatal("expected error for --batch-size=0")
	}
}

func TestRunRetryAllFailed_DryRun_RequiresPostgres(t *testing.T) {
	// Dry-run still needs a DSN to list restaurants from the DB.
	viper.Set("POSTGRES_DSN", "")
	t.Cleanup(func() { viper.Set("POSTGRES_DSN", "") })
	if os.Getenv("POSTGRES_DSN") != "" {
		t.Skip("POSTGRES_DSN is set; skipping the missing-DSN test")
	}

	cmd := newRetryAllFailedTestCmd()
	if err := cmd.Flags().Set("dry-run", "true"); err != nil {
		t.Fatalf("setting flag: %v", err)
	}

	err := runRetryAllFailed(cmd, nil)
	if err == nil {
		t.Fatal("expected error: dry-run still requires --postgres-dsn")
	}
}
