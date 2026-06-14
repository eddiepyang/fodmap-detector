package menutracking

import (
	"context"
	"testing"

	"golang.org/x/time/rate"
)

func TestNewDomainLimiterMap(t *testing.T) {
	m := NewDomainLimiterMap(rate.Limit(5), 2)
	if m == nil {
		t.Fatal("NewDomainLimiterMap returned nil")
	}
	if m.limiters == nil {
		t.Fatal("limiters map not initialized")
	}
	if m.r != rate.Limit(5) {
		t.Errorf("rate: got %v, want 5", m.r)
	}
	if m.burst != 2 {
		t.Errorf("burst: got %d, want 2", m.burst)
	}
}

func TestDomainLimiterMap_Seed(t *testing.T) {
	m := NewDomainLimiterMap(rate.Limit(1), 1)
	m.Seed([]string{"epa.gov", "fda.gov"})

	if len(m.limiters) != 2 {
		t.Fatalf("expected 2 seeded limiters, got %d", len(m.limiters))
	}
	if _, ok := m.limiters["epa.gov"]; !ok {
		t.Error("epa.gov limiter not seeded")
	}
	if _, ok := m.limiters["fda.gov"]; !ok {
		t.Error("fda.gov limiter not seeded")
	}
}

func TestDomainLimiterMap_GetCreatesAndReuses(t *testing.T) {
	m := NewDomainLimiterMap(rate.Limit(1), 1)

	l1 := m.get("epa.gov")
	if l1 == nil {
		t.Fatal("get returned nil limiter")
	}
	if len(m.limiters) != 1 {
		t.Errorf("expected 1 limiter after first get, got %d", len(m.limiters))
	}

	// Second call for the same domain must reuse the existing limiter.
	l2 := m.get("epa.gov")
	if l1 != l2 {
		t.Error("get created a new limiter instead of reusing the existing one")
	}
}

func TestDomainLimiterMap_Wait(t *testing.T) {
	// A generous burst means the first Wait succeeds immediately.
	m := NewDomainLimiterMap(rate.Limit(10), 5)

	if err := m.Wait(context.Background(), "epa.gov"); err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}
	// The domain should now have a lazily created limiter.
	if _, ok := m.limiters["epa.gov"]; !ok {
		t.Error("Wait did not create a limiter for the domain")
	}
}

func TestDomainLimiterMap_WaitContextCancelled(t *testing.T) {
	// Burst of 0 means no token is ever available, so Wait must respect
	// the cancelled context and return its error.
	m := NewDomainLimiterMap(rate.Limit(1), 0)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := m.Wait(ctx, "epa.gov"); err == nil {
		t.Error("expected error from Wait with cancelled context, got nil")
	}
}
