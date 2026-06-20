package menutracking

import (
	"context"
	"fmt"
	"time"

	"fodmap/menutracking/store"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Source represents a regulatory data source to be scraped periodically.
type Source struct {
	ID           string
	Name         string
	URL          string
	Domain       string
	Tier         string // gov | consultancy | commercial
	CronSchedule string // e.g. "@daily", "0 6 * * 1-5"
	MaxTokens    int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ListSources returns all sources, ordered by domain.
func ListSources(ctx context.Context, pool *pgxpool.Pool) ([]Source, error) {
	rows, err := pool.Query(ctx, store.ListSourcesSQL)
	if err != nil {
		return nil, fmt.Errorf("listing sources: %w", err)
	}
	defer rows.Close()

	var sources []Source
	for rows.Next() {
		var s Source
		if err := rows.Scan(&s.ID, &s.Name, &s.URL, &s.Domain, &s.Tier, &s.CronSchedule, &s.MaxTokens, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning source: %w", err)
		}
		sources = append(sources, s)
	}
	return sources, rows.Err()
}

// SourceByID returns a source by its ID.
func SourceByID(ctx context.Context, pool *pgxpool.Pool, id string) (Source, error) {
	var s Source
	err := pool.QueryRow(ctx, store.SourceByIDSQL, id).
		Scan(&s.ID, &s.Name, &s.URL, &s.Domain, &s.Tier, &s.CronSchedule, &s.MaxTokens, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return s, fmt.Errorf("getting source %s: %w", id, err)
	}
	return s, nil
}

// InsertSource upserts a source. The ID is generated deterministically from
// the domain so re-inserts are idempotent.
func InsertSource(ctx context.Context, pool *pgxpool.Pool, s *Source) error {
	if s.ID == "" {
		s.ID = uuid.NewSHA1(uuid.NameSpaceURL, []byte(s.Domain)).String()
	}
	now := time.Now()
	s.CreatedAt = now
	s.UpdatedAt = now
	_, err := pool.Exec(ctx, store.InsertSourceSQL,
		s.ID, s.Name, s.URL, s.Domain, s.Tier, s.CronSchedule, s.MaxTokens, s.CreatedAt, s.UpdatedAt)
	if err != nil {
		return fmt.Errorf("inserting source: %w", err)
	}
	return nil
}
