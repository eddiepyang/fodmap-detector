package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestParseCronSchedule(t *testing.T) {
	tests := []struct {
		name    string
		expr    string
		want    time.Duration
		wantErr bool
	}{
		{"daily", "@daily", 24 * time.Hour, false},
		{"midnight", "@midnight", 24 * time.Hour, false},
		{"daily cron", "0 0 * * *", 24 * time.Hour, false},
		{"hourly", "@hourly", 1 * time.Hour, false},
		{"hourly cron", "0 * * * *", 1 * time.Hour, false},
		{"weekly", "@weekly", 7 * 24 * time.Hour, false},
		{"weekly cron", "0 0 * * 0", 7 * 24 * time.Hour, false},
		{"unsupported", "*/5 * * * *", 0, true},
		{"empty", "", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sched, err := parseCronSchedule(tt.expr)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseCronSchedule(%q): expected error, got nil", tt.expr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCronSchedule(%q): unexpected error: %v", tt.expr, err)
			}

			// PeriodicInterval reports the next run as now + interval, so the
			// returned schedule's interval can be recovered by diffing.
			now := time.Now()
			next := sched.Next(now)
			if got := next.Sub(now); got != tt.want {
				t.Errorf("parseCronSchedule(%q) interval: got %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}

// newAddSourceTestCmd builds a command with the same flags init() registers on
// menutrackingAddSourceCmd, so runMenutrackingAddSource can be exercised in
// isolation without mutating global command state.
func newAddSourceTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "add-source"}
	// Execute() would normally seed a non-nil context; runMenutrackingAddSource
	// reads cmd.Context() and passes it to pgxpool.New, which spawns a goroutine
	// that calls context.WithCancel and panics on a nil parent.
	cmd.SetContext(context.Background())
	cmd.Flags().String("name", "", "")
	cmd.Flags().String("tier", "gov", "")
	cmd.Flags().String("cron", "@weekly", "")
	cmd.Flags().Int("max-tokens", 32000, "")
	return cmd
}

func TestRunMenutrackingAddSourceRejectsInvalidCron(t *testing.T) {
	// A parseable DSN that points nowhere. With MinConns=0 the pool creates no
	// idle connections, so the cron guard returns before any dial is attempted.
	viper.Set("postgres-dsn", "postgres://u:p@127.0.0.1:1/none")
	t.Cleanup(func() { viper.Set("postgres-dsn", "") })

	cmd := newAddSourceTestCmd()
	if err := cmd.Flags().Set("cron", "every-tuesday"); err != nil {
		t.Fatalf("setting cron flag: %v", err)
	}

	err := runMenutrackingAddSource(cmd, []string{"https://example.gov/rules"})
	if err == nil {
		t.Fatal("expected error for invalid cron, got nil")
	}
	if !strings.Contains(err.Error(), "invalid --cron") {
		t.Fatalf("expected invalid --cron error, got: %v", err)
	}
}
