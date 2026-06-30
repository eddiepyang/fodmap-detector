package store

import (
	"strings"
	"testing"
)

func TestRenderListDiscardedJobsSQL_DefaultSchema(t *testing.T) {
	SetRiverSchema("river")
	sql := RenderListDiscardedJobsSQL()
	if !strings.Contains(sql, `FROM "river".river_job`) {
		t.Errorf("expected FROM \"river\".river_job, got:\n%s", sql)
	}
	if !strings.Contains(sql, "WHERE state = 'discarded'") {
		t.Errorf("expected WHERE clause, got:\n%s", sql)
	}
}

func TestRenderListDiscardedJobsSQL_CustomSchema(t *testing.T) {
	SetRiverSchema("my_river")
	sql := RenderListDiscardedJobsSQL()
	if !strings.Contains(sql, `FROM "my_river".river_job`) {
		t.Errorf("expected FROM \"my_river\".river_job, got:\n%s", sql)
	}
	// Restore default for other tests.
	SetRiverSchema("river")
}

func TestRenderListDiscardedJobsSQL_Quoting(t *testing.T) {
	// Schema names with embedded quotes must be escaped to prevent injection.
	SetRiverSchema(`a"b`)
	sql := RenderListDiscardedJobsSQL()
	if !strings.Contains(sql, `"a""b".river_job`) {
		t.Errorf("expected escaped identifier, got:\n%s", sql)
	}
	SetRiverSchema("river")
}

func TestSetRiverSchema_EmptyIgnored(t *testing.T) {
	SetRiverSchema("custom")
	SetRiverSchema("") // empty must not override
	if RiverSchema() != "custom" {
		t.Errorf("empty SetRiverSchema should be ignored; got %q", RiverSchema())
	}
	SetRiverSchema("river")
}

func TestSetRiverSchema_WhitespaceTrimmed(t *testing.T) {
	SetRiverSchema("  river  ")
	if RiverSchema() != "river" {
		t.Errorf("expected trimmed 'river', got %q", RiverSchema())
	}
}
