package store

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"fodmap/data"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) (*FodmapCatalogStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	return &FodmapCatalogStore{db: db}, mock
}

func TestFodmapCatalogStore_Create(t *testing.T) {
	store, mock := newTestStore(t)
	defer func() { _ = store.Close() }()

	entry := CatalogEntry{
		Ingredient:    "Garlic",
		Level:         "high",
		Groups:        []string{"fructans"},
		Notes:         "Strong",
		Substitutions: []string{"garlic oil"},
	}

	mock.ExpectExec("INSERT INTO fodmap_catalog").
		WithArgs("garlic", "high", sqlmock.AnyArg(), "Strong", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := store.Create(context.Background(), entry)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFodmapCatalogStore_CreateConflict(t *testing.T) {
	store, mock := newTestStore(t)
	defer func() { _ = store.Close() }()

	entry := CatalogEntry{
		Ingredient: "Garlic",
		Level:      "high",
	}

	mock.ExpectExec("INSERT INTO fodmap_catalog").
		WithArgs("garlic", "high", sqlmock.AnyArg(), "", sqlmock.AnyArg()).
		WillReturnError(&pgconn.PgError{Code: "23505"})

	err := store.Create(context.Background(), entry)
	assert.ErrorIs(t, err, ErrIngredientExists)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFodmapCatalogStore_Get(t *testing.T) {
	store, mock := newTestStore(t)
	defer func() { _ = store.Close() }()

	now := time.Now()
	mock.ExpectQuery("SELECT ingredient, level, groups, notes, substitutions, updated_at FROM fodmap_catalog").
		WithArgs("garlic").
		WillReturnRows(sqlmock.NewRows([]string{"ingredient", "level", "groups", "notes", "substitutions", "updated_at"}).
			AddRow("garlic", "high", "{fructans}", "Strong", "{garlic oil}", now))

	entry, err := store.Get(context.Background(), "Garlic")
	require.NoError(t, err)
	require.NotNil(t, entry)
	assert.Equal(t, "garlic", entry.Ingredient)
	assert.Equal(t, "high", entry.Level)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFodmapCatalogStore_GetNotFound(t *testing.T) {
	store, mock := newTestStore(t)
	defer func() { _ = store.Close() }()

	mock.ExpectQuery("SELECT ingredient, level, groups, notes, substitutions, updated_at FROM fodmap_catalog").
		WithArgs("garlic").
		WillReturnError(sql.ErrNoRows)

	entry, err := store.Get(context.Background(), "Garlic")
	assert.NoError(t, err)
	assert.Nil(t, entry)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFodmapCatalogStore_List(t *testing.T) {
	store, mock := newTestStore(t)
	defer func() { _ = store.Close() }()

	filter := ListFilter{Search: "garlic", Level: "high", Group: "fructans"}
	mock.ExpectQuery("SELECT ingredient, level, groups, notes, substitutions, updated_at FROM fodmap_catalog").
		WithArgs("%garlic%", "high", "fructans", 20, 0).
		WillReturnRows(sqlmock.NewRows([]string{"ingredient", "level", "groups", "notes", "substitutions", "updated_at"}).
			AddRow("garlic", "high", "{fructans}", "", "{}", time.Now()))

	entries, err := store.List(context.Background(), 0, 20, filter)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
	assert.Equal(t, "garlic", entries[0].Ingredient)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFodmapCatalogStore_Count(t *testing.T) {
	store, mock := newTestStore(t)
	defer func() { _ = store.Close() }()

	filter := ListFilter{Level: "high"}
	mock.ExpectQuery("SELECT COUNT").
		WithArgs("high").
		WillReturnRows(sqlmock.NewRows([]string{"total"}).AddRow(42))

	total, err := store.Count(context.Background(), filter)
	assert.NoError(t, err)
	assert.Equal(t, 42, total)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFodmapCatalogStore_Stats(t *testing.T) {
	store, mock := newTestStore(t)
	defer func() { _ = store.Close() }()

	mock.ExpectQuery("SELECT").
		WillReturnRows(sqlmock.NewRows([]string{"total_count", "level_counts", "group_counts"}).
			AddRow(100, []byte(`{"high": 60, "low": 40}`), []byte(`{"fructans": 50}`)))

	stats, err := store.Stats(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 100, stats.TotalCount)
	assert.Equal(t, 60, stats.LevelCounts["high"])
	assert.Equal(t, 40, stats.LevelCounts["low"])
	assert.Equal(t, 50, stats.GroupCounts["fructans"])
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFodmapCatalogStore_Update(t *testing.T) {
	store, mock := newTestStore(t)
	defer func() { _ = store.Close() }()

	entry := CatalogEntry{
		Level:  "low",
		Groups: []string{},
		Notes:  "Safe",
	}

	mock.ExpectExec("UPDATE fodmap_catalog").
		WithArgs("garlic", "low", sqlmock.AnyArg(), "Safe", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := store.Update(context.Background(), "Garlic", entry)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFodmapCatalogStore_UpdateNotFound(t *testing.T) {
	store, mock := newTestStore(t)
	defer func() { _ = store.Close() }()

	entry := CatalogEntry{Level: "low"}
	mock.ExpectExec("UPDATE fodmap_catalog").
		WithArgs("garlic", "low", sqlmock.AnyArg(), "", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := store.Update(context.Background(), "Garlic", entry)
	assert.ErrorIs(t, err, ErrIngredientNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFodmapCatalogStore_Delete(t *testing.T) {
	store, mock := newTestStore(t)
	defer func() { _ = store.Close() }()

	mock.ExpectExec("DELETE FROM fodmap_catalog").
		WithArgs("garlic").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := store.Delete(context.Background(), "Garlic")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFodmapCatalogStore_ListAll(t *testing.T) {
	store, mock := newTestStore(t)
	defer func() { _ = store.Close() }()

	mock.ExpectQuery("SELECT ingredient, level, groups, notes, substitutions, updated_at FROM fodmap_catalog").
		WillReturnRows(sqlmock.NewRows([]string{"ingredient", "level", "groups", "notes", "substitutions", "updated_at"}).
			AddRow("garlic", "high", "{fructans}", "", "{}", time.Now()))

	entries, err := store.ListAll(context.Background())
	require.NoError(t, err)
	assert.Len(t, entries, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFodmapCatalogStore_Seed(t *testing.T) {
	store, mock := newTestStore(t)
	defer func() { _ = store.Close() }()

	items := map[string]data.FodmapEntry{
		"garlic": {Level: "high", Groups: []string{"fructans"}},
	}

	mock.ExpectBegin()
	mock.ExpectPrepare("INSERT INTO fodmap_catalog")
	mock.ExpectExec("INSERT INTO fodmap_catalog").
		WithArgs("garlic", "high", sqlmock.AnyArg(), "", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO fodmap_meta").
		WithArgs("seeded", "true").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := store.Seed(context.Background(), items)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFodmapCatalogStore_Reseed(t *testing.T) {
	store, mock := newTestStore(t)
	defer func() { _ = store.Close() }()

	items := map[string]data.FodmapEntry{
		"garlic": {Level: "high", Groups: []string{"fructans"}},
	}

	mock.ExpectBegin()
	mock.ExpectPrepare("INSERT INTO fodmap_catalog")
	mock.ExpectExec("INSERT INTO fodmap_catalog").
		WithArgs("garlic", "high", sqlmock.AnyArg(), "", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	count, err := store.Reseed(context.Background(), items)
	assert.NoError(t, err)
	assert.Equal(t, 1, count)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFodmapCatalogStore_IsSeeded(t *testing.T) {
	store, mock := newTestStore(t)
	defer func() { _ = store.Close() }()

	mock.ExpectQuery("SELECT value FROM fodmap_meta").
		WithArgs("seeded").
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("true"))

	seeded, err := store.IsSeeded(context.Background())
	assert.NoError(t, err)
	assert.True(t, seeded)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFodmapCatalogStore_IsSeededNotFound(t *testing.T) {
	store, mock := newTestStore(t)
	defer func() { _ = store.Close() }()

	mock.ExpectQuery("SELECT value FROM fodmap_meta").
		WithArgs("seeded").
		WillReturnError(sql.ErrNoRows)

	seeded, err := store.IsSeeded(context.Background())
	assert.NoError(t, err)
	assert.False(t, seeded)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestToMap(t *testing.T) {
	entries := []CatalogEntry{
		{
			Ingredient:    "garlic",
			Level:         "high",
			Groups:        []string{"fructans"},
			Notes:         "Strong",
			Substitutions: []string{"garlic oil"},
		},
	}
	m := ToMap(entries)
	assert.Equal(t, data.FodmapEntry{
		Level:         "high",
		Groups:        []string{"fructans"},
		Notes:         "Strong",
		Substitutions: []string{"garlic oil"},
	}, m["garlic"])
}

func TestParsePGArray(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "empty", input: "{}", want: []string{}},
		{name: "empty string", input: "", want: []string{}},
		{name: "NULL", input: "NULL", want: []string{}},
		{name: "single element", input: "{fructans}", want: []string{"fructans"}},
		{name: "multiple elements", input: "{fructans,GOS,lactose}", want: []string{"fructans", "GOS", "lactose"}},
		{name: "quoted element with space", input: `{"excess fructose"}`, want: []string{"excess fructose"}},
		{name: "mixed quoted and unquoted", input: `{"excess fructose",sorbitol}`, want: []string{"excess fructose", "sorbitol"}},
		{name: "quoted element with comma", input: `{"garlic-infused olive oil","garlic chives","asafoetida powder (small amount, resin form)"}`, want: []string{"garlic-infused olive oil", "garlic chives", "asafoetida powder (small amount, resin form)"}},
		{name: "escaped quote", input: `{"it\'s","hello"}`, want: []string{"it's", "hello"}},
		{name: "complex substitutions", input: `{"canned chickpeas (small amount, rinsed)","firm tofu"}`, want: []string{"canned chickpeas (small amount, rinsed)", "firm tofu"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parsePGArray(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPgxStringArrayScan(t *testing.T) {
	var a pgxStringArray

	// Test scanning a PG array string with quoted elements
	err := a.Scan(`{"excess fructose","GOS",lactose}`)
	require.NoError(t, err)
	assert.Equal(t, pgxStringArray{"excess fructose", "GOS", "lactose"}, a)

	// Test scanning nil
	err = a.Scan(nil)
	require.NoError(t, err)
	assert.Nil(t, a)

	// Test scanning []string
	err = a.Scan([]string{"a", "b"})
	require.NoError(t, err)
	assert.Equal(t, pgxStringArray{"a", "b"}, a)

	// Test empty array
	err = a.Scan("{}")
	require.NoError(t, err)
	assert.Equal(t, pgxStringArray{}, a)
}

func TestScanEntriesWithQuotedArrays(t *testing.T) {
	store, mock := newTestStore(t)
	defer func() { _ = store.Close() }()

	now := time.Now()

	// Simulate PostgreSQL returning TEXT[] with quoted elements containing
	// commas and spaces — exactly the format that caused the original bug.
	mock.ExpectQuery("SELECT ingredient, level, groups, notes, substitutions, updated_at FROM fodmap_catalog").
		WillReturnRows(sqlmock.NewRows([]string{"ingredient", "level", "groups", "notes", "substitutions", "updated_at"}).
			AddRow("garlic", "high", `{fructans}`, "", `{}`, now).
			AddRow("agave", "high", `{"excess fructose"}`, "Very high in excess fructose", `{"pure maple syrup","table sugar","rice malt syrup"}`, now).
			AddRow("apple juice", "high", `{"excess fructose",sorbitol}`, "Concentrated", `{"water (with lemon)","cranberry juice (small amount)"}`, now).
			AddRow("garlic chives", "low", `{}`, "", `{}`, now).
			AddRow("coconut milk", "low", `{}`, "", `{}`, now))

	entries, err := store.ListAll(context.Background())
	require.NoError(t, err)
	assert.Len(t, entries, 5)

	// garlic: simple unquoted group
	assert.Equal(t, []string{"fructans"}, entries[0].Groups)
	assert.Equal(t, []string{}, entries[0].Substitutions)

	// agave: quoted group with space + quoted substitutions with spaces
	assert.Equal(t, []string{"excess fructose"}, entries[1].Groups)
	assert.Equal(t, []string{"pure maple syrup", "table sugar", "rice malt syrup"}, entries[1].Substitutions)

	// apple juice: mixed quoted/unquoted groups + substitutions with parens
	assert.Equal(t, []string{"excess fructose", "sorbitol"}, entries[2].Groups)
	assert.Equal(t, []string{"water (with lemon)", "cranberry juice (small amount)"}, entries[2].Substitutions)

	// garlic chives: empty arrays
	assert.Equal(t, []string{}, entries[3].Groups)
	assert.Equal(t, []string{}, entries[3].Substitutions)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSeedNormalizesNilSlices(t *testing.T) {
	store, mock := newTestStore(t)
	defer func() { _ = store.Close() }()

	// Test that nil Groups and Substitutions are normalized to empty slices
	// before being passed to pq.Array, which would otherwise produce SQL NULL.
	items := map[string]data.FodmapEntry{
		"tofu": {Level: "low"}, // nil Groups and Substitutions
	}

	mock.ExpectBegin()
	mock.ExpectPrepare("INSERT INTO fodmap_catalog")
	mock.ExpectExec("INSERT INTO fodmap_catalog").
		WithArgs("tofu", "low", sqlmock.AnyArg(), "", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO fodmap_meta").
		WithArgs("seeded", "true").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := store.Seed(context.Background(), items)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestReseedNormalizesNilSlices(t *testing.T) {
	store, mock := newTestStore(t)
	defer func() { _ = store.Close() }()

	items := map[string]data.FodmapEntry{
		"tofu": {Level: "low"}, // nil Groups and Substitutions
	}

	mock.ExpectBegin()
	mock.ExpectPrepare("INSERT INTO fodmap_catalog")
	mock.ExpectExec("INSERT INTO fodmap_catalog").
		WithArgs("tofu", "low", sqlmock.AnyArg(), "", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	count, err := store.Reseed(context.Background(), items)
	assert.NoError(t, err)
	assert.Equal(t, 1, count)
	assert.NoError(t, mock.ExpectationsWereMet())
}
