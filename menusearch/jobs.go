package menusearch

type DiscoverMenuURLArgs struct {
	CAMIS   string `json:"camis"`
	DBA     string `json:"dba"`
	Address string `json:"address"`
	Attempt int    `json:"attempt"`
}

func (DiscoverMenuURLArgs) Kind() string {
	return "discover_menu_url"
}

type ScrapeMenuArgs struct {
	CAMIS   string `json:"camis"`
	URL     string `json:"url"`
	DBA     string `json:"dba"`
	Attempt int    `json:"attempt"`
}

func (ScrapeMenuArgs) Kind() string {
	return "scrape_menu"
}
