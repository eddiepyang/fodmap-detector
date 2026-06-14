package menutracking

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// FastPathResult is the outcome of applying an active extraction rule to a
// scraped page. If the rule matches and produces valid JSON, Extracted holds
// the parsed StructuredUpdate; otherwise it is nil and the caller should
// fall back to the agent path.
type FastPathResult struct {
	Extracted *StructuredUpdate
	Raw       string // raw extracted text before JSON parsing
}

// ApplyRule attempts to apply an active ExtractionRule to the page content.
// If the rule's selector matches and the extracted text parses as valid JSON
// conforming to StructuredUpdate, it returns the parsed result. Otherwise it
// returns a nil Extracted, signalling the caller to use the agent path.
func ApplyRule(ctx context.Context, pool *pgxpool.Pool, domain, pageContent string) (*FastPathResult, error) {
	rule, err := GetActiveRule(ctx, pool, domain)
	if err != nil {
		return nil, fmt.Errorf("fast path: %w", err)
	}
	if rule == nil {
		// No active rule for this domain — fall through to agent path.
		return &FastPathResult{}, nil
	}

	// The selector field currently represents a simple string-matching
	// heuristic (e.g. a CSS selector or JSON path). Phase 1 uses a basic
	// substring/contains match for government API responses that are already
	// structured. A full CSS/JSON path engine can be layered in later.
	extracted := applySelector(pageContent, rule.Selector)
	if extracted == "" {
		return &FastPathResult{}, nil
	}

	var update StructuredUpdate
	if err := json.Unmarshal([]byte(extracted), &update); err != nil {
		return &FastPathResult{Raw: extracted}, nil
	}

	// Basic validation: substance name and change type must be non-empty.
	if update.SubstanceName == "" || update.ChangeType == "" {
		return &FastPathResult{Raw: extracted}, nil
	}

	return &FastPathResult{Extracted: &update, Raw: extracted}, nil
}

// ApplyRuleWithSelector applies a specific ExtractionRule to page content.
// Used by RulePromotionWorker to verify a proposed rule against live content.
func ApplyRuleWithSelector(ctx context.Context, pool *pgxpool.Pool, rule *ExtractionRule, pageContent string) (*FastPathResult, error) {
	extracted := applySelector(pageContent, rule.Selector)
	if extracted == "" {
		return &FastPathResult{}, nil
	}

	var update StructuredUpdate
	if err := json.Unmarshal([]byte(extracted), &update); err != nil {
		return &FastPathResult{Raw: extracted}, nil
	}

	if update.SubstanceName == "" || update.ChangeType == "" {
		return &FastPathResult{Raw: extracted}, nil
	}

	return &FastPathResult{Extracted: &update, Raw: extracted}, nil
}

// applySelector extracts content from pageContent using the given selector.
// Phase 1 supports a simple "json:KEY" prefix for extracting a value from
// JSON responses, and falls back to the full content for empty selectors.
func applySelector(pageContent, selector string) string {
	if selector == "" {
		return pageContent
	}
	if strings.HasPrefix(selector, "json:") {
		key := strings.TrimPrefix(selector, "json:")
		var m map[string]any
		if err := json.Unmarshal([]byte(pageContent), &m); err != nil {
			return ""
		}
		v, ok := m[key]
		if !ok {
			return ""
		}
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(b)
	}
	// Future: CSS selector against HTML. For now, return empty to trigger
	// the agent path.
	return ""
}
