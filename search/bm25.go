package search

import (
	"math"
	"strings"
)

// bm25Score returns a simplified BM25-style keyword relevance score between query and text.
// It counts matching query terms weighted by IDF (log(1 + 1/df)) approximation,
// normalized by the number of query terms. Returns 0 when there is no overlap.
func bm25Score(query, text string) float64 {
	if query == "" || text == "" {
		return 0
	}

	queryTerms := tokenize(query)
	if len(queryTerms) == 0 {
		return 0
	}
	textTerms := tokenize(text)
	if len(textTerms) == 0 {
		return 0
	}

	// Build term frequency map for text.
	tf := make(map[string]int, len(textTerms))
	for _, t := range textTerms {
		tf[t]++
	}

	docLen := float64(len(textTerms))
	const (
		k1        = 1.2
		b         = 0.75
		avgDocLen = 100.0 // assumed average document length for normalization
	)

	var score float64
	for _, term := range queryTerms {
		freq := float64(tf[term])
		if freq == 0 {
			continue
		}
		// Simplified IDF: higher weight for rare terms (here approximated as log(2)).
		idf := math.Log(2)
		tfNorm := freq * (k1 + 1) / (freq + k1*(1-b+b*docLen/avgDocLen))
		score += idf * tfNorm
	}

	// Normalize by query length so longer queries don't dominate.
	return score / float64(len(queryTerms))
}

// blendScore combines a dense vector score with a BM25 keyword score.
// alpha=0 (unset) or alpha=1 returns the pure dense score (no keyword blending).
// alpha between 0 (exclusive) and 1 (exclusive) blends both: higher alpha weights dense more.
func blendScore(query, text string, denseScore float64, alpha float32) float64 {
	if alpha <= 0 || alpha >= 1 {
		return denseScore
	}
	return float64(alpha)*denseScore + float64(1-alpha)*bm25Score(query, text)
}

// tokenize splits text into lowercase tokens, stripping punctuation.
func tokenize(s string) []string {
	s = strings.ToLower(s)
	var tokens []string
	var cur strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			cur.WriteRune(r)
		} else if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	return tokens
}
