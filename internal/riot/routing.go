package riot

// RegionFor maps a platform (na1, euw1, kr, ...) to the regional routing
// value used by account-v1 and match-v5.
func RegionFor(platform string) (string, bool) {
	r, ok := map[string]string{
		"na1": "americas", "br1": "americas", "la1": "americas", "la2": "americas",
		"kr": "asia", "jp1": "asia",
		"euw1": "europe", "eun1": "europe", "tr1": "europe", "ru": "europe", "me1": "europe",
		"oc1": "sea", "sg2": "sea", "tw2": "sea", "vn2": "sea",
	}[platform]
	return r, ok
}

// Queue ids for ranked Summoner's Rift.
const (
	QueueSoloDuo = 420
	QueueFlex    = 440
)

// Platform is a server players pick, for a settings menu.
type Platform struct{ ID, Name string }

// Platforms lists every platform RegionFor knows, in menu order.
var Platforms = []Platform{
	{"na1", "North America"},
	{"euw1", "Europe West"},
	{"eun1", "Europe Nordic & East"},
	{"kr", "Korea"},
	{"br1", "Brazil"},
	{"la1", "Latin America North"},
	{"la2", "Latin America South"},
	{"oc1", "Oceania"},
	{"jp1", "Japan"},
	{"tr1", "Türkiye"},
	{"ru", "Russia"},
	{"me1", "Middle East"},
	{"sg2", "Southeast Asia"},
	{"tw2", "Taiwan"},
	{"vn2", "Vietnam"},
}
