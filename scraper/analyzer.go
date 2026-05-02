package scraper

import (
	"context"
	"fmt"
	"strings"

	"fodmap/data"
)

// Analyzer cross-references extracted menu items against the FODMAP ingredient
// database and produces a safety score for each item.
type Analyzer struct{}

// NewAnalyzer creates an Analyzer.
func NewAnalyzer() *Analyzer { return &Analyzer{} }

// Analyze takes a list of raw MenuItems and returns a MenuAnalysis with
// FODMAP flags and safety scores.
func (a *Analyzer) Analyze(_ context.Context, businessName, menuURL string, items []MenuItem) *MenuAnalysis {
	analyzed := make([]AnalyzedMenuItem, 0, len(items))
	safeCount := 0

	for _, item := range items {
		flags := a.flagIngredients(item.Ingredients, item.Name, item.Description)
		score := scoreFromFlags(flags)
		if score == "safe" {
			safeCount++
		}
		analyzed = append(analyzed, AnalyzedMenuItem{
			MenuItem:    item,
			FodmapFlags: flags,
			SafetyScore: score,
		})
	}

	total := len(items)
	summary := ""
	if total > 0 {
		pct := 100 * safeCount / total
		summary = fmt.Sprintf(summaryTemplate(pct), safeCount, total, pct)
	}

	return &MenuAnalysis{
		BusinessName: businessName,
		MenuURL:      menuURL,
		Items:        analyzed,
		Summary:      summary,
	}
}

// flagIngredients checks a list of ingredient strings (and falls back to
// scanning item name + description) against the FODMAP database.
func (a *Analyzer) flagIngredients(ingredients []string, name, description string) []FodmapFlag {
	var flags []FodmapFlag
	seen := make(map[string]bool)

	check := func(term string) {
		term = strings.TrimSpace(strings.ToLower(term))
		if term == "" || seen[term] {
			return
		}
		seen[term] = true

		// Direct lookup in the static FodmapDB map.
		entry, ok := data.FodmapDB[term]
		if !ok || entry.Level == "low" {
			return
		}
		flags = append(flags, FodmapFlag{
			Ingredient: term,
			Level:      entry.Level,
			Notes:      entry.Notes,
		})
	}

	if len(ingredients) > 0 {
		for _, ing := range ingredients {
			for _, part := range strings.Split(ing, ",") {
				check(part)
			}
		}
	} else {
		// No explicit ingredient list — scan the item name and description.
		for _, word := range tokenize(name + " " + description) {
			check(word)
		}
	}
	return flags
}

// tokenize splits text into lowercase words, stripping punctuation.
func tokenize(text string) []string {
	text = strings.ToLower(text)
	var words []string
	for _, w := range strings.FieldsFunc(text, func(r rune) bool {
		return r == ' ' || r == ',' || r == '.' || r == ';' || r == '/' || r == '(' || r == ')'
	}) {
		if len(w) > 2 {
			words = append(words, w)
		}
	}
	return words
}

// scoreFromFlags derives a safety score from a set of FODMAP flags.
func scoreFromFlags(flags []FodmapFlag) string {
	if len(flags) == 0 {
		return "safe"
	}
	for _, f := range flags {
		if f.Level == "high" {
			return "avoid"
		}
	}
	return "caution"
}

func summaryTemplate(pct int) string {
	switch {
	case pct >= 80:
		return "%d of %d items appear FODMAP-safe (%d%%) — great options here!"
	case pct >= 50:
		return "%d of %d items appear FODMAP-safe (%d%%) — moderate options available."
	default:
		return "%d of %d items appear FODMAP-safe (%d%%) — proceed with caution and ask about modifications."
	}
}
