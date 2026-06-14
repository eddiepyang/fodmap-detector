package cli

import (
	"testing"
	"time"
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
