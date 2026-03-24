package cli

import "testing"

func TestLookupFODMAP_HighExact(t *testing.T) {
	result := lookupFODMAP("garlic")
	if result["found"] != true {
		t.Fatalf("found = %v, want true", result["found"])
	}
	if result["fodmap_level"] != "high" {
		t.Errorf("fodmap_level = %v, want high", result["fodmap_level"])
	}
	groups, ok := result["fodmap_groups"].([]string)
	if !ok || len(groups) == 0 {
		t.Errorf("expected non-empty fodmap_groups, got %v", result["fodmap_groups"])
	}
}

func TestLookupFODMAP_Low(t *testing.T) {
	result := lookupFODMAP("rice")
	if result["found"] != true {
		t.Fatalf("found = %v, want true", result["found"])
	}
	if result["fodmap_level"] != "low" {
		t.Errorf("fodmap_level = %v, want low", result["fodmap_level"])
	}
}

func TestLookupFODMAP_Moderate(t *testing.T) {
	result := lookupFODMAP("peas")
	if result["found"] != true {
		t.Fatalf("found = %v, want true", result["found"])
	}
	if result["fodmap_level"] != "moderate" {
		t.Errorf("fodmap_level = %v, want moderate", result["fodmap_level"])
	}
}

func TestLookupFODMAP_NotFound(t *testing.T) {
	result := lookupFODMAP("unobtainium")
	if result["found"] != false {
		t.Errorf("found = %v, want false", result["found"])
	}
	if result["message"] == nil {
		t.Error("expected a message for unknown ingredient")
	}
}

func TestLookupFODMAP_CaseInsensitive(t *testing.T) {
	result := lookupFODMAP("GARLIC")
	if result["found"] != true {
		t.Errorf("case-insensitive lookup failed: found = %v", result["found"])
	}
}

func TestLookupFODMAP_LeadingTrailingSpace(t *testing.T) {
	result := lookupFODMAP("  garlic  ")
	if result["found"] != true {
		t.Errorf("whitespace-trimmed lookup failed: found = %v", result["found"])
	}
}

func TestLookupFODMAP_PartialMatch(t *testing.T) {
	// "garlic powder" should match "garlic" via substring matching.
	result := lookupFODMAP("garlic powder")
	if result["found"] != true {
		t.Errorf("partial match failed: found = %v", result["found"])
	}
	if result["fodmap_level"] != "high" {
		t.Errorf("fodmap_level = %v, want high", result["fodmap_level"])
	}
}

func TestLookupFODMAP_GroupsEmptySliceNotNil(t *testing.T) {
	// Low FODMAP entries should return [] not nil for fodmap_groups.
	result := lookupFODMAP("rice")
	groups, ok := result["fodmap_groups"].([]string)
	if !ok {
		t.Fatalf("fodmap_groups not []string, got %T", result["fodmap_groups"])
	}
	if groups == nil {
		t.Error("fodmap_groups should be [] not nil")
	}
}

func TestLookupFODMAP_Notes(t *testing.T) {
	result := lookupFODMAP("garlic")
	if result["notes"] == nil || result["notes"] == "" {
		t.Error("expected notes for garlic, got none")
	}
}

func TestLookupFODMAP_NoNotesForSimpleEntry(t *testing.T) {
	// Rice has no notes; the key should be absent.
	result := lookupFODMAP("rice")
	if _, ok := result["notes"]; ok {
		t.Errorf("expected no notes key for rice, got %q", result["notes"])
	}
}
