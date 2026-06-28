package cli

import (
	"testing"

	"fodmap/menusearch"
)

func TestPaginateRecords(t *testing.T) {
	// Create a dummy slice of records for testing.
	var dummyRecords []menusearch.NYCRestaurantRecord
	for i := 0; i < 50; i++ {
		dummyRecords = append(dummyRecords, menusearch.NYCRestaurantRecord{})
	}

	tests := []struct {
		name       string
		records    []menusearch.NYCRestaurantRecord
		limit      int
		offset     int
		wantLength int
	}{
		{
			name:       "No limit or offset (assuming limit 0 means no limit for slicing logic)",
			records:    dummyRecords,
			limit:      0,
			offset:     0,
			wantLength: 50,
		},
		{
			name:       "Limit only",
			records:    dummyRecords,
			limit:      10,
			offset:     0,
			wantLength: 10,
		},
		{
			name:       "Offset only",
			records:    dummyRecords,
			limit:      0,
			offset:     10,
			wantLength: 40,
		},
		{
			name:       "Limit and offset",
			records:    dummyRecords,
			limit:      10,
			offset:     10,
			wantLength: 10,
		},
		{
			name:       "Offset out of bounds",
			records:    dummyRecords,
			limit:      10,
			offset:     100,
			wantLength: 0,
		},
		{
			name:       "Limit larger than remaining",
			records:    dummyRecords,
			limit:      100,
			offset:     45,
			wantLength: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := paginateRecords(tt.records, tt.limit, tt.offset)
			if len(got) != tt.wantLength {
				t.Errorf("paginateRecords() returned %d records, want %d", len(got), tt.wantLength)
			}
			if tt.wantLength > 0 {
				// Verify it's the correct slice by checking pointers or just trusting the length for this simple test.
				// Since all are empty structs, length is sufficient.
				// But we can check if it's a subslice by verifying capacity or pointers if we gave them IDs.
			}
		})
	}
}
