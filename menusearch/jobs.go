package menusearch

type DiscoverMenuURLArgs struct {
	CAMIS    string `json:"camis"`
	DBA      string `json:"dba"`
	Building string `json:"building"`
	Street   string `json:"street"`
	Boro     string `json:"boro"`
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
}

func (ScrapeMenuArgs) Kind() string {
	return "menusearch.scrape_menu"
}
