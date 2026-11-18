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
