package menutracking

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"fodmap/chat"
	"fodmap/scraper"
	"fodmap/search"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// stubFetcher implements scraper.Fetcher for testing.
type stubFetcher struct {
	body   string
	ct     string
	err    error
	called int
}

func (f *stubFetcher) Fetch(_ context.Context, url string) (scraper.FetchResult, error) {
	f.called++
	if f.err != nil {
		return scraper.FetchResult{}, f.err
	}
	r, w := io.Pipe()
	go func() {
		defer func() { _ = w.Close() }()
		_, _ = w.Write([]byte(f.body))
	}()
	return scraper.FetchResult{Body: r, ContentType: f.ct}, nil
}

func (f *stubFetcher) Close() error { return nil }

// stubVectorSink implements VectorSink for testing.
type stubVectorSink struct {
	upserted int
}

func (s *stubVectorSink) BatchUpsertRegulatory(_ context.Context, _ []search.RegulatoryUpdate) error {
	s.upserted++
	return nil
}

// stubRiverInserter implements RiverInserter for testing.
type stubRiverInserter struct {
	inserted []river.JobArgs
}

func (r *stubRiverInserter) Insert(_ context.Context, args river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	r.inserted = append(r.inserted, args)
	return &rivertype.JobInsertResult{Job: &rivertype.JobRow{}}, nil
}

// stubChatBackend returns structured updates.
type stubChatBackend struct {
	msg chat.Message
	err error
}

func (b *stubChatBackend) Generate(_ context.Context, _ chat.GenerateOpts) (chat.Message, error) {
	return b.msg, b.err
}

func newScrapeJob(args ScrapeJobArgs) *river.Job[ScrapeJobArgs] {
	return &river.Job[ScrapeJobArgs]{
		JobRow: &rivertype.JobRow{
			ID:      int64(uuid.New().ID()),
			Attempt: 1,
			Kind:    args.Kind(),
		},
		Args: args,
	}
}

func TestScrapeWorker_FastPathHit(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()

	ctx := context.Background()
	domain := "gov.example"

	src := &Source{URL: "https://gov.example", Domain: domain, Tier: "gov", CronSchedule: "@weekly"}
	if err := InsertSource(ctx, pool, src); err != nil {
		t.Fatalf("InsertSource: %v", err)
	}

	rule := &ExtractionRule{
		Domain:     domain,
		Selector:   "json:update",
		Fields:     map[string]string{},
		Provenance: "https://gov.example",
	}
	if err := InsertProposedRule(ctx, pool, rule); err != nil {
		t.Fatalf("InsertProposedRule: %v", err)
	}
	if err := PromoteRule(ctx, pool, rule.ID); err != nil {
		t.Fatalf("PromoteRule: %v", err)
	}

	tmpDir := t.TempDir()
	oldBronzeDir := BronzeDir
	BronzeDir = tmpDir
	defer func() { BronzeDir = oldBronzeDir }()

	fetcher := &stubFetcher{
		body: `{"update": {"cas_number": "50-00-0", "substance_name": "formaldehyde", "change_type": "addition", "description": "test", "effective_date": "2026-01-01", "source_url": "https://gov.example"}}`,
		ct:   "application/json",
	}
	sink := &stubVectorSink{}
	inserter := &stubRiverInserter{}

	w := &ScrapeWorker{
		Pool:         pool,
		Fetcher:      fetcher,
		RateLimiters: NewDomainLimiterMap(1000, 1),
		AgentConfig:  DefaultAgentPathConfig(),
		VectorSink:   sink,
		RiverClient:  inserter,
	}

	job := newScrapeJob(ScrapeJobArgs{SourceID: src.ID, URL: "https://gov.example/page", Domain: domain})

	if err := w.Work(ctx, job); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if fetcher.called != 1 {
		t.Errorf("expected 1 fetch, got %d", fetcher.called)
	}
	if sink.upserted != 1 {
		t.Errorf("expected 1 vector upsert, got %d", sink.upserted)
	}
	if len(inserter.inserted) != 0 {
		t.Errorf("expected no promotion job, got %d", len(inserter.inserted))
	}

	var count int
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM regulatory_updates").Scan(&count); err != nil {
		t.Fatalf("count updates: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 update in postgres, got %d", count)
	}
}

func TestScrapeWorker_AgentPath(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()

	ctx := context.Background()
	domain := "agent.example"

	src := &Source{URL: "https://agent.example", Domain: domain, Tier: "gov", CronSchedule: "@weekly"}
	if err := InsertSource(ctx, pool, src); err != nil {
		t.Fatalf("InsertSource: %v", err)
	}

	tmpDir := t.TempDir()
	oldBronzeDir := BronzeDir
	BronzeDir = tmpDir
	defer func() { BronzeDir = oldBronzeDir }()

	fetcher := &stubFetcher{body: "<html>page content</html>", ct: "text/html"}
	sink := &stubVectorSink{}
	inserter := &stubRiverInserter{}

	update := StructuredUpdate{
		CASNumber:     "71-43-2",
		SubstanceName: "benzene",
		ChangeType:    ChangeTypeRestriction,
		Description:   "new limit",
		EffectiveDate: "2026-02-01",
		SourceURL:     "https://agent.example/page",
	}
	updateJSON, _ := json.Marshal(update)

	w := &ScrapeWorker{
		Pool:         pool,
		Fetcher:      fetcher,
		RateLimiters: NewDomainLimiterMap(1000, 1),
		AgentConfig:  DefaultAgentPathConfig(),
		VectorSink:   sink,
		RiverClient:  inserter,
		ChatBackend:  &stubChatBackend{msg: chat.Message{Text: string(updateJSON)}},
	}

	job := newScrapeJob(ScrapeJobArgs{SourceID: src.ID, URL: "https://agent.example/page", Domain: domain})

	if err := w.Work(ctx, job); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if sink.upserted != 1 {
		t.Errorf("expected 1 vector upsert, got %d", sink.upserted)
	}

	var count int
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM regulatory_updates").Scan(&count); err != nil {
		t.Fatalf("count updates: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 update in postgres, got %d", count)
	}
}

func TestScrapeWorker_NoUpdate(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()

	ctx := context.Background()
	domain := "empty.example"

	tmpDir := t.TempDir()
	oldBronzeDir := BronzeDir
	BronzeDir = tmpDir
	defer func() { BronzeDir = oldBronzeDir }()

	fetcher := &stubFetcher{body: "{}", ct: "application/json"}
	w := &ScrapeWorker{
		Pool:         pool,
		Fetcher:      fetcher,
		RateLimiters: NewDomainLimiterMap(1000, 1),
		AgentConfig:  DefaultAgentPathConfig(),
		ChatBackend:  &stubChatBackend{msg: chat.Message{Text: "{}"}},
	}

	job := newScrapeJob(ScrapeJobArgs{SourceID: "src3", URL: "https://empty.example/page", Domain: domain})

	if err := w.Work(ctx, job); err == nil {
		t.Fatal("expected error when no update produced")
	}
}

func TestScrapeWorker_FetchError(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()

	w := &ScrapeWorker{
		Pool:         pool,
		Fetcher:      &stubFetcher{err: errors.New("boom")},
		RateLimiters: NewDomainLimiterMap(1000, 1),
	}

	job := newScrapeJob(ScrapeJobArgs{SourceID: "src4", URL: "https://fail.example", Domain: "fail.example"})

	if err := w.Work(context.Background(), job); err == nil {
		t.Fatal("expected error from fetch failure")
	}
}

func TestWriteBronzeFile_PermissionError(t *testing.T) {
	// Use a read-only directory to force a write error.
	tmpDir := t.TempDir()
	if err := os.Chmod(tmpDir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	defer func() { _ = os.Chmod(tmpDir, 0o755) }()

	path := filepath.Join(tmpDir, "bronze", "test.html")
	if err := writeBronzeFile(path, []byte("x")); err == nil {
		t.Error("expected permission error")
	}
}

func TestUpsertUpdate(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()

	ctx := context.Background()

	// upsertUpdate references sources(id), so create a source first.
	src := &Source{URL: "https://gov.example", Domain: "gov.example", Tier: "gov", CronSchedule: "@weekly"}
	if err := InsertSource(ctx, pool, src); err != nil {
		t.Fatalf("InsertSource: %v", err)
	}

	u := &StructuredUpdate{
		CASNumber:     "50-00-0",
		SubstanceName: "formaldehyde",
		ChangeType:    ChangeTypeAddition,
		Description:   "test",
		EffectiveDate: "2026-01-01",
		SourceURL:     "https://gov.example",
	}
	if err := upsertUpdate(ctx, pool, src.ID, "bronze/test.html", u); err != nil {
		t.Fatalf("upsertUpdate: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM regulatory_updates").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row, got %d", count)
	}
}
