package menusearch

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

type NYCRestaurantRecord struct {
	CAMIS              string
	DBA                string
	Boro               string
	Building           string
	Street             string
	Zipcode            string
	Phone              string
	CuisineDescription string
	InspectionDate     string
	Latitude           float64
	Longitude          float64
	NTA                string
	RecordDate         string
}

func parseDate(dateStr string) time.Time {
	if dateStr == "" {
		return time.Time{}
	}
	t, err := time.Parse("01/02/2006", dateStr)
	if err == nil {
		return t
	}
	t, err = time.Parse(time.RFC3339, dateStr)
	if err == nil {
		return t
	}
	// Try parsing Socrata default API format
	t, err = time.Parse("2006-01-02T15:04:05.000", dateStr)
	if err == nil {
		return t
	}
	return time.Time{}
}

// ParseNYCCSV reads the NYC OpenData CSV and returns deduplicated records
// keyed by CAMIS. When multiple rows share a CAMIS, the one with the most
// recent inspection_date wins.
func ParseNYCCSV(r io.Reader) ([]NYCRestaurantRecord, error) {
	cr := csv.NewReader(r)

	header, err := cr.Read()
	if err != nil {
		if err == io.EOF {
			return nil, nil
		}
		return nil, err
	}

	colIdx := make(map[string]int)
	for i, h := range header {
		colIdx[h] = i
	}

	if _, ok := colIdx["camis"]; !ok {
		return nil, fmt.Errorf("missing camis column in csv")
	}

	dedup := make(map[string]NYCRestaurantRecord)

	for {
		row, err := cr.Read()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}

		getCol := func(name string) string {
			if idx, ok := colIdx[name]; ok && idx < len(row) {
				return strings.TrimSpace(row[idx])
			}
			return ""
		}

		camis := getCol("camis")
		if camis == "" {
			continue
		}

		boro := getCol("boro")
		if boro == "0" {
			continue
		}

		latStr := getCol("latitude")
		lonStr := getCol("longitude")

		lat, _ := strconv.ParseFloat(latStr, 64)
		lon, _ := strconv.ParseFloat(lonStr, 64)

		if lat == 0 {
			continue
		}

		rec := NYCRestaurantRecord{
			CAMIS:              camis,
			DBA:                getCol("dba"),
			Boro:               boro,
			Building:           getCol("building"),
			Street:             getCol("street"),
			Zipcode:            getCol("zipcode"),
			Phone:              getCol("phone"),
			CuisineDescription: getCol("cuisine_description"),
			InspectionDate:     getCol("inspection_date"),
			Latitude:           lat,
			Longitude:          lon,
			NTA:                getCol("nta"),
			RecordDate:         getCol("record_date"),
		}

		if existing, ok := dedup[camis]; ok {
			t1 := parseDate(rec.InspectionDate)
			t2 := parseDate(existing.InspectionDate)

			if t1.After(t2) {
				dedup[camis] = rec
			}
		} else {
			dedup[camis] = rec
		}
	}

	var results []NYCRestaurantRecord
	for _, v := range dedup {
		results = append(results, v)
	}

	return results, nil
}
