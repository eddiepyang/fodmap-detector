package menutracking

import (
	"context"
	"fmt"
	"time"

	"fodmap/menutracking/store"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RuleStatus represents the lifecycle of an extraction rule.
type RuleStatus string

const (
	RuleStatusProposed RuleStatus = "proposed"
	RuleStatusActive   RuleStatus = "active"
	RuleStatusRejected RuleStatus = "rejected"
)

// ExtractionRule is a CSS selector or JSON path that the fast path applies to
// scraped pages from a given domain.
type ExtractionRule struct {
	ID          string
	Domain      string
	Selector    string
	Fields      map[string]string // field name → path mapping
	Status      RuleStatus
	Provenance  string
	ProposedAt  time.Time
	ActivatedAt *time.Time
	CreatedAt   time.Time
}

// InsertProposedRule writes a proposed rule to extraction_rules. If a rule with
// the same ID already exists (deterministic from domain+selector), it is
// updated rather than rejected — making re-scrapes idempotent.
func InsertProposedRule(ctx context.Context, pool *pgxpool.Pool, r *ExtractionRule) error {
	if r.ID == "" {
		r.ID = uuid.NewSHA1(uuid.NameSpaceURL, []byte(r.Domain+r.Selector)).String()
	}
	if r.Status == "" {
		r.Status = RuleStatusProposed
	}
	now := time.Now()
	r.ProposedAt = now
	r.CreatedAt = now
	_, err := pool.Exec(ctx, store.InsertProposedRuleSQL,
		r.ID, r.Domain, r.Selector, r.Fields, r.Status, r.Provenance, r.ProposedAt, r.ActivatedAt, r.CreatedAt)
	if err != nil {
		return fmt.Errorf("inserting proposed rule: %w", err)
	}
	return nil
}

// GetActiveRule returns the active extraction rule for the given domain, or
// (nil, nil) if no active rule exists. This allows callers to distinguish
// between "no rule found" (normal fast-path miss) and a real database error.
func GetActiveRule(ctx context.Context, pool *pgxpool.Pool, domain string) (*ExtractionRule, error) {
	var r ExtractionRule
	err := pool.QueryRow(ctx, store.GetActiveRuleSQL, domain).
		Scan(&r.ID, &r.Domain, &r.Selector, &r.Fields, &r.Status, &r.Provenance, &r.ProposedAt, &r.ActivatedAt, &r.CreatedAt)
	if err == nil {
		return &r, nil
	}
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return nil, fmt.Errorf("getting active rule for %s: %w", domain, err)
}

// GetProposedRule returns a proposed extraction rule by its ID, or (nil, nil) if
// no proposed rule exists with that ID.
func GetProposedRule(ctx context.Context, pool *pgxpool.Pool, ruleID string) (*ExtractionRule, error) {
	var r ExtractionRule
	err := pool.QueryRow(ctx, store.GetProposedRuleSQL, ruleID).
		Scan(&r.ID, &r.Domain, &r.Selector, &r.Fields, &r.Status, &r.Provenance, &r.ProposedAt, &r.ActivatedAt, &r.CreatedAt)
	if err == nil {
		return &r, nil
	}
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return nil, fmt.Errorf("getting proposed rule %s: %w", ruleID, err)
}

// PromoteRule marks a proposed rule as active after verification.
func PromoteRule(ctx context.Context, pool *pgxpool.Pool, ruleID string) error {
	now := time.Now()
	result, err := pool.Exec(ctx, store.PromoteRuleSQL, now, ruleID)
	if err != nil {
		return fmt.Errorf("promoting rule %s: %w", ruleID, err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("rule %s not found or not in proposed status", ruleID)
	}
	return nil
}

// RejectRule marks a proposed rule as rejected after it fails verification.
func RejectRule(ctx context.Context, pool *pgxpool.Pool, ruleID string) error {
	result, err := pool.Exec(ctx, store.RejectRuleSQL, ruleID)
	if err != nil {
		return fmt.Errorf("rejecting rule %s: %w", ruleID, err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("rule %s not found or not in proposed status", ruleID)
	}
	return nil
}
