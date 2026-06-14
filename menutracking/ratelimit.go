package menutracking

import (
	"context"
	"sync"

	"golang.org/x/time/rate"
)

// DomainLimiterMap holds per-domain rate limiters seeded from the sources
// table. The source set is essentially static, so no eviction logic is
// needed — this is the opposite of the IP-based limiter in server/middleware
// which grows without bound and uses non-blocking Allow().
type DomainLimiterMap struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	r        rate.Limit
	burst    int
}

// NewDomainLimiterMap creates an empty map. New limiters are created on first
// access with the given rate (tokens per second) and burst.
func NewDomainLimiterMap(r rate.Limit, burst int) *DomainLimiterMap {
	return &DomainLimiterMap{
		limiters: make(map[string]*rate.Limiter),
		r:        r,
		burst:    burst,
	}
}

// Seed populates the map with one limiter per domain. Called once at boot
// after loading sources from the database.
func (m *DomainLimiterMap) Seed(domains []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range domains {
		m.limiters[d] = rate.NewLimiter(m.r, m.burst)
	}
}

// Wait blocks until the limiter for the given domain allows a request, or the
// context is cancelled. It is safe to call from multiple goroutines.
func (m *DomainLimiterMap) Wait(ctx context.Context, domain string) error {
	limiter := m.get(domain)
	return limiter.Wait(ctx)
}

// get returns the limiter for domain, creating one if it doesn't exist.
func (m *DomainLimiterMap) get(domain string) *rate.Limiter {
	m.mu.Lock()
	defer m.mu.Unlock()
	if l, ok := m.limiters[domain]; ok {
		return l
	}
	l := rate.NewLimiter(m.r, m.burst)
	m.limiters[domain] = l
	return l
}
