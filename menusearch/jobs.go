package menusearch

type DiscoverMenuURLArgs struct {
	CAMIS    string `json:"camis"`
	DBA      string `json:"dba"`
	Building string `json:"building"`
	Street   string `json:"street"`
	Boro     string `json:"boro"`
	Zipcode  string `json:"zipcode"`
	Attempt  int    `json:"attempt"`
}

func (DiscoverMenuURLArgs) Kind() string {
	return "menusearch.discover_menu_url"
}

type ScrapeMenuArgs struct {
	CAMIS            string `json:"camis"`
	URL              string `json:"url"`
	DBA              string `json:"dba"`
	DiscoveryEventID string `json:"discovery_event_id"`
	// Depth tracks how many directory-expansion levels have been traversed.
	// Existing River jobs deserialise to 0 (the zero value). Only Depth==0 jobs
	// perform directory expansion; sub-URL fetches are done inline at depth 1 and
	// never recurse further.
	Depth int `json:"depth"`
}

func (ScrapeMenuArgs) Kind() string {
	return "menusearch.scrape_menu"
}
