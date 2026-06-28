package menusearch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func FetchNYCRestaurants(ctx context.Context, area string, appToken string) (io.ReadCloser, error) {
	af, ok := Areas[area]
	if !ok {
		return nil, fmt.Errorf("unknown area: %q", area)
	}

	var conditions []string
	if len(af.NTAs) > 0 {
		var quotedNTAs []string
		for _, nta := range af.NTAs {
			quotedNTAs = append(quotedNTAs, fmt.Sprintf("'%s'", nta))
		}
		conditions = append(conditions, fmt.Sprintf("nta IN (%s)", strings.Join(quotedNTAs, ",")))
	}

	for nta, zips := range af.NTAZipRestrict {
		var quotedZips []string
		for _, zip := range zips {
			quotedZips = append(quotedZips, fmt.Sprintf("'%s'", zip))
		}
		conditions = append(conditions, fmt.Sprintf("(nta='%s' AND zipcode IN (%s))", nta, strings.Join(quotedZips, ",")))
	}

	if len(conditions) == 0 {
		return nil, fmt.Errorf("area %q has no NTA or zip restrictions defined", area)
	}
	soqlWhere := strings.Join(conditions, " OR ")

	u, err := url.Parse("https://data.cityofnewyork.us/resource/43nn-pn8j.csv")
	if err != nil {
		return nil, err
	}

	q := u.Query()
	q.Set("$where", soqlWhere)
	q.Set("$select", "camis,dba,boro,building,street,zipcode,phone,cuisine_description,inspection_date,latitude,longitude,nta,record_date")
	q.Set("$limit", "50000") // safe cap
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, err
	}

	if appToken != "" {
		req.Header.Set("X-App-Token", appToken)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("socrata API returned status %d", resp.StatusCode)
	}

	return resp.Body, nil
}
