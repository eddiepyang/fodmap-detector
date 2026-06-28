package menusearch

type AreaFilter struct {
	NTAs           []string
	NTAZipRestrict map[string][]string
}

var Areas = map[string]AreaFilter{
	"astoria-lic": {
		NTAs: []string{"QN70", "QN71", "QN72", "QN68"},
		NTAZipRestrict: map[string][]string{
			"QN31": {"11101", "11109"},
		},
	},
}
