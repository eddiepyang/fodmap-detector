package menutracking

import (
	"context"
	"testing"
)

func TestInsertAndProposedRule(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()

	ctx := context.Background()
	r := &ExtractionRule{
		Domain:     "fda.gov",
		Selector:   "json:results",
		Fields:     map[string]string{"change_type": "json:change_type"},
		Provenance: "https://fda.gov/food",
	}
	if err := InsertProposedRule(ctx, pool, r); err != nil {
		t.Fatalf("InsertProposedRule: %v", err)
	}
	if r.ID == "" {
		t.Fatal("expected ID assigned after insert")
	}
	if r.Status != RuleStatusProposed {
		t.Errorf("expected status proposed, got %q", r.Status)
	}
	if r.ProposedAt.IsZero() || r.CreatedAt.IsZero() {
		t.Error("expected ProposedAt and CreatedAt set")
	}

	got, err := ProposedRule(ctx, pool, r.ID)
	if err != nil {
		t.Fatalf("ProposedRule: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil proposed rule")
	}
	if got.Selector != "json:results" {
		t.Errorf("Selector: got %q, want %q", got.Selector, "json:results")
	}
}

func TestActiveRule_NotFound(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()

	got, err := ActiveRule(context.Background(), pool, "no-such-domain.example")
	if err != nil {
		t.Fatalf("ActiveRule: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil rule, got %+v", got)
	}
}

func TestPromoteAndRejectRule(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()

	ctx := context.Background()
	r := &ExtractionRule{
		Domain:     "fda.gov",
		Selector:   "json:results",
		Fields:     map[string]string{},
		Provenance: "https://fda.gov/food",
	}
	if err := InsertProposedRule(ctx, pool, r); err != nil {
		t.Fatalf("InsertProposedRule: %v", err)
	}

	if err := PromoteRule(ctx, pool, r.ID); err != nil {
		t.Fatalf("PromoteRule: %v", err)
	}

	active, err := ActiveRule(ctx, pool, "fda.gov")
	if err != nil {
		t.Fatalf("ActiveRule: %v", err)
	}
	if active == nil {
		t.Fatal("expected active rule after promotion")
	}
	if active.Status != RuleStatusActive {
		t.Errorf("expected status active, got %q", active.Status)
	}
	if active.ActivatedAt == nil || active.ActivatedAt.IsZero() {
		t.Error("expected ActivatedAt set")
	}

	// Rejecting an already-active rule should fail (RowsAffected == 0).
	if err := RejectRule(ctx, pool, r.ID); err == nil {
		t.Error("expected RejectRule to fail on active rule")
	}
}

func TestRejectProposedRule(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()

	ctx := context.Background()
	r := &ExtractionRule{
		Domain:     "epa.gov",
		Selector:   "div.content",
		Fields:     map[string]string{},
		Provenance: "https://epa.gov",
	}
	if err := InsertProposedRule(ctx, pool, r); err != nil {
		t.Fatalf("InsertProposedRule: %v", err)
	}
	insertedID := r.ID

	if err := RejectRule(ctx, pool, insertedID); err != nil {
		t.Fatalf("RejectRule: %v", err)
	}

	// get_proposed_rule filters on status='proposed', so the rejected rule is
	// intentionally not returned. Query the DB directly to verify status.
	var status string
	if err := pool.QueryRow(ctx, "SELECT status FROM extraction_rules WHERE id = $1", insertedID).Scan(&status); err != nil {
		t.Fatalf("query rejected rule: %v", err)
	}
	if status != string(RuleStatusRejected) {
		t.Errorf("expected status rejected, got %q", status)
	}
}

func TestPromoteRule_NotFound(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()

	err := PromoteRule(context.Background(), pool, "00000000-0000-0000-0000-000000000000")
	if err == nil {
		t.Fatal("expected error for missing rule")
	}
}
