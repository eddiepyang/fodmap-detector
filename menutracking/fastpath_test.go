package menutracking

import (
	"context"
	"testing"
)

func TestApplyRule_NoActiveRule(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()

	res, err := ApplyRule(context.Background(), pool, "unknown.example", "{}: content")
	if err != nil {
		t.Fatalf("ApplyRule: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	if res.Extracted != nil {
		t.Error("expected nil Extracted when no active rule")
	}
}

func TestApplyRule_JSONKey(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()

	ctx := context.Background()
	r := &ExtractionRule{
		Domain:     "gov.example",
		Selector:   "json:update",
		Fields:     map[string]string{},
		Provenance: "https://gov.example",
	}
	if err := InsertProposedRule(ctx, pool, r); err != nil {
		t.Fatalf("InsertProposedRule: %v", err)
	}
	if err := PromoteRule(ctx, pool, r.ID); err != nil {
		t.Fatalf("PromoteRule: %v", err)
	}

	content := `{"update": {"cas_number": "50-00-0", "substance_name": "formaldehyde", "change_type": "addition", "description": "test", "source_url": "https://gov.example"}}`
	res, err := ApplyRule(ctx, pool, "gov.example", content)
	if err != nil {
		t.Fatalf("ApplyRule: %v", err)
	}
	if res.Extracted == nil {
		t.Fatal("expected extracted update")
	}
	if res.Extracted.SubstanceName != "formaldehyde" {
		t.Errorf("SubstanceName: got %q, want %q", res.Extracted.SubstanceName, "formaldehyde")
	}
}

func TestApplyRule_InvalidJSON(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()

	ctx := context.Background()
	r := &ExtractionRule{
		Domain:     "gov.example",
		Selector:   "json:update",
		Fields:     map[string]string{},
		Provenance: "https://gov.example",
	}
	if err := InsertProposedRule(ctx, pool, r); err != nil {
		t.Fatalf("InsertProposedRule: %v", err)
	}
	if err := PromoteRule(ctx, pool, r.ID); err != nil {
		t.Fatalf("PromoteRule: %v", err)
	}

	// Valid JSON selector but extracted object is missing required fields.
	content := `{"update": {"substance_name": "", "change_type": ""}}`
	res, err := ApplyRule(ctx, pool, "gov.example", content)
	if err != nil {
		t.Fatalf("ApplyRule: %v", err)
	}
	if res.Extracted != nil {
		t.Error("expected nil Extracted for invalid update")
	}
	if res.Raw == "" {
		t.Error("expected Raw populated")
	}
}

func TestApplySelector_EmptySelector(t *testing.T) {
	content := "plain text"
	got := applySelector(content, "")
	if got != content {
		t.Errorf("expected full content, got %q", got)
	}
}

func TestApplySelector_BadJSON(t *testing.T) {
	got := applySelector("not-json", "json:key")
	if got != "" {
		t.Errorf("expected empty for bad JSON, got %q", got)
	}
}
