package menusearch

import (
	"bytes"
	"testing"
)

func TestParseNYCCSV_Valid(t *testing.T) {
	csvData := `camis,dba,boro,building,street,zipcode,phone,cuisine_description,inspection_date,action,violation_code,violation_description,critical_flag,score,grade,grade_date,record_date,inspection_type,latitude,longitude,community_board,council_district,census_tract,bin,bbl,nta,location_point1
50014830,JUDY & PUNCH,Astoria,34-08,30TH AVE,11103,7186265500,American,2023-01-01,Violations were cited,04L,Evidence of mice,Y,12,A,2023-01-01,2024-01-01,Initial,40.765,-73.921,401,22,100,123,456,Astoria,Point
`
	records, err := ParseNYCCSV(bytes.NewReader([]byte(csvData)))
	if err != nil {
		t.Fatalf("ParseNYCCSV failed: %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	rec := records[0]
	if rec.CAMIS != "50014830" {
		t.Errorf("got CAMIS %q, want 50014830", rec.CAMIS)
	}
	if rec.DBA != "JUDY & PUNCH" {
		t.Errorf("got DBA %q, want JUDY & PUNCH", rec.DBA)
	}
	if rec.Latitude != 40.765 {
		t.Errorf("got Latitude %f, want 40.765", rec.Latitude)
	}
}

func TestParseNYCCSV_Invalid(t *testing.T) {
	csvData := `camis,dba
50014830,JUDY & PUNCH,Extra Column`
	_, err := ParseNYCCSV(bytes.NewReader([]byte(csvData)))
	if err == nil {
		t.Fatalf("ParseNYCCSV expected error on invalid CSV, got nil")
	}
}
