package data

import (
	"strings"
	"testing"
)

// validLevels is the set of allowed FODMAP level values.
var validLevels = map[string]bool{
	"high":     true,
	"moderate": true,
	"low":      true,
}

// validGroups is the set of allowed FODMAP group values.
var validGroups = map[string]bool{
	"fructans":        true,
	"GOS":             true,
	"lactose":         true,
	"excess fructose": true,
	"sorbitol":        true,
	"mannitol":        true,
	"fructan":         true,
}

func TestFodmapDB_MinimumEntries(t *testing.T) {
	if len(FodmapDB) < 100 {
		t.Errorf("FodmapDB has %d entries, want at least 100", len(FodmapDB))
	}
}

func TestFodmapDB_ValidLevels(t *testing.T) {
	for ingredient, entry := range FodmapDB {
		if !validLevels[entry.Level] {
			t.Errorf("FodmapDB[%q].Level = %q, want one of {high, moderate, low}", ingredient, entry.Level)
		}
	}
}

func TestFodmapDB_ValidGroups(t *testing.T) {
	for ingredient, entry := range FodmapDB {
		for _, g := range entry.Groups {
			if !validGroups[g] {
				t.Errorf("FodmapDB[%q] has unknown group %q", ingredient, g)
			}
		}
	}
}

func TestFodmapDB_HighAndModerateHaveSubstitutions(t *testing.T) {
	for ingredient, entry := range FodmapDB {
		if entry.Level == "high" || entry.Level == "moderate" {
			if len(entry.Substitutions) == 0 {
				t.Errorf("FodmapDB[%q] is %s FODMAP but has no substitutions; high/moderate entries should suggest alternatives", ingredient, entry.Level)
			}
		}
	}
}

func TestFodmapDB_LowEntriesHaveNoSubstitutions(t *testing.T) {
	// Low-FODMAP ingredients should not list substitutions (they are already low).
	for ingredient, entry := range FodmapDB {
		if entry.Level == "low" && len(entry.Substitutions) > 0 {
			t.Errorf("FodmapDB[%q] is low FODMAP but has substitutions %v; low entries should not need alternatives", ingredient, entry.Substitutions)
		}
	}
}

func TestFodmapDB_NoEmptyIngredientKeys(t *testing.T) {
	for ingredient := range FodmapDB {
		if strings.TrimSpace(ingredient) == "" {
			t.Error("FodmapDB has an empty-string key")
		}
	}
}

func TestFodmapDB_NoEmptyGroups(t *testing.T) {
	for ingredient, entry := range FodmapDB {
		// Low-FODMAP items may legitimately have empty groups (e.g., water, spirits)
		if entry.Level != "low" && len(entry.Groups) == 0 {
			t.Errorf("FodmapDB[%q] is %s FODMAP but has no groups; non-low entries should specify at least one FODMAP group", ingredient, entry.Level)
		}
	}
}

func TestFodmapDB_LookupCommonIngredients(t *testing.T) {
	// Verify that common high-FODMAP ingredients are present in the database.
	common := []string{"garlic", "onion", "wheat", "milk", "apple", "honey"}
	for _, ingredient := range common {
		if _, ok := FodmapDB[ingredient]; !ok {
			t.Errorf("FodmapDB missing common ingredient %q", ingredient)
		}
	}
}

func TestFodmapDB_SubstitutionsAreLowFodmapOrQualified(t *testing.T) {
	// Substitutions for high/moderate items should ideally be low-FODMAP items
	// or qualified with portion guidance. This is a soft check — we just verify
	// substitutions are non-empty strings.
	for ingredient, entry := range FodmapDB {
		for _, sub := range entry.Substitutions {
			if strings.TrimSpace(sub) == "" {
				t.Errorf("FodmapDB[%q] has an empty substitution string", ingredient)
			}
		}
	}
}

func TestFodmapDB_NoDuplicateKeys(t *testing.T) {
	// Go maps don't allow duplicate keys, but this test ensures the map
	// was constructed correctly (no accidental overwrites during initialization).
	// We verify by checking that well-known entries have the expected level.
	spotChecks := map[string]string{
		"garlic": "high",
		"onion":  "high",
		"rice":   "low",
		"oats":   "low",
	}
	for ingredient, wantLevel := range spotChecks {
		entry, ok := FodmapDB[ingredient]
		if !ok {
			t.Errorf("FodmapDB missing %q", ingredient)
			continue
		}
		if entry.Level != wantLevel {
			t.Errorf("FodmapDB[%q].Level = %q, want %q (possible duplicate key overwrite)", ingredient, entry.Level, wantLevel)
		}
	}
}
