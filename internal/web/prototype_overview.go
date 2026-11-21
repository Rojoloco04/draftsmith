package web

// PROTOTYPE: three divergent overview redesigns (bold visual direction,
// death heatmap across games, champion pool constellation), switchable
// on the real overview route with ?variant=A|B|C (0 is the current page).
// Throwaway: once a design is picked, fold the winner into overview.html
// properly and delete this file, prototype_overview.html, prototype.css,
// prototype.js and db/prototype_deaths.go. Verdict goes in
// internal/web/PROTOTYPE_NOTES.md.

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/rojoloco04/draftsmith/internal/analysis"
)

// protoVariants are the switcher's stops, in order.
var protoVariants = []struct{ Key, Name string }{
	{"0", "Current"},
	{"A", "War room"},
	{"B", "Editorial"},
	{"C", "Hextech"},
}

// protoVariant is the requested prototype variant, "" for none.
func protoVariant(r *http.Request) string {
	v := strings.ToUpper(r.URL.Query().Get("variant"))
	for _, p := range protoVariants {
		if p.Key == v {
			return v
		}
	}
	return ""
}

type protoDeath struct {
	X       int     `json:"x"`
	Y       int     `json:"y"`
	Min     float64 `json:"m"`
	Win     bool    `json:"w"`
	Blue    bool    `json:"b"`
	Champ   string  `json:"c"`
	MatchID string  `json:"id"`
}

type protoChamp struct {
	Key    string  `json:"key"`
	Name   string  `json:"name"`
	Icon   string  `json:"icon"`
	Splash string  `json:"splash"`
	Games  int     `json:"games"`
	Wins   int     `json:"wins"`
	WR     float64 `json:"wr"`
	Adj    float64 `json:"adj"`
	KDA    float64 `json:"kda"`
	CSM    float64 `json:"csm"`
	Role   string  `json:"role"`
	First  string  `json:"first"`
	Last   string  `json:"last"`
}

type protoGame struct {
	ID    string `json:"id"`
	Champ string `json:"champ"`
	Icon  string `json:"icon"`
	Win   bool   `json:"win"`
	K     int    `json:"k"`
	D     int    `json:"d"`
	A     int    `json:"a"`
	Date  string `json:"date"`
	Role  string `json:"role"`
	GD15  *int   `json:"gd15"`
}

type protoTrans struct {
	A string `json:"a"`
	B string `json:"b"`
	N int    `json:"n"`
}

type protoPayload struct {
	Map    string       `json:"map"`
	Base   float64      `json:"base"`
	Deaths []protoDeath `json:"deaths"`
	Champs []protoChamp `json:"champs"`
	Trans  []protoTrans `json:"trans"`
	Games  []protoGame  `json:"games"`
}

type protoData struct {
	overviewData
	Variant  string
	Variants []struct{ Key, Name string }
	JSON     string
	Top      *protoChamp
	Champs   []protoChamp
	Deaths   int
	Recent20 []protoGame
	Wins20   int
	// RankList is the selected account's rank, or each account's.
	RankList []acctRank
}

// protoOverview renders a prototype variant of the overview.
func (s *Server) protoOverview(w http.ResponseWriter, r *http.Request, d overviewData, variant string) {
	sum := d.Sum
	in := map[string]analysis.Game{}
	for _, g := range sum.Games {
		in[g.MatchID] = g
	}
	all, err := s.db.ProtoDeaths(r.Context())
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err)
		return
	}
	p := protoPayload{Map: "https://ddragon.leagueoflegends.com/cdn/" + ddVersion(d.Patch) + "/img/map/map11.png", Base: sum.Base}
	for _, x := range all {
		g, ok := in[x.MatchID]
		if !ok {
			continue
		}
		p.Deaths = append(p.Deaths, protoDeath{X: x.X, Y: x.Y, Min: x.Minute, Win: g.Win, Blue: x.Blue, Champ: champName(g.Champion), MatchID: x.MatchID})
	}

	roles := map[string]map[string]int{}
	first, last := map[string]string{}, map[string]string{}
	for _, g := range sum.Games {
		if roles[g.Champion] == nil {
			roles[g.Champion] = map[string]int{}
			first[g.Champion] = g.Start.Format("Jan 2006")
		}
		roles[g.Champion][roleKey(g.Role)]++
		last[g.Champion] = g.Start.Format("Jan 2, 2006")
	}
	for _, c := range champRows(sum.Games, sum.Base) {
		role, n := "", 0
		for k, v := range roles[c.Key] {
			if v > n || (v == n && k < role) {
				role, n = k, v
			}
		}
		p.Champs = append(p.Champs, protoChamp{
			Key: c.Key, Name: champName(c.Key), Icon: champImg(c.Key, d.Patch), Splash: splashImg(c.Key),
			Games: c.Games, Wins: c.Wins, WR: c.WinRate(), Adj: c.Adjusted, KDA: c.KDA(), CSM: c.CSPerMin(),
			Role: role, First: first[c.Key], Last: last[c.Key],
		})
	}

	pairs := map[[2]string]int{}
	for i := 1; i < len(sum.Games); i++ {
		a, b := sum.Games[i-1].Champion, sum.Games[i].Champion
		if a == b {
			continue
		}
		if a > b {
			a, b = b, a
		}
		pairs[[2]string{a, b}]++
	}
	for k, n := range pairs {
		p.Trans = append(p.Trans, protoTrans{k[0], k[1], n})
	}
	sort.Slice(p.Trans, func(i, j int) bool { return p.Trans[i].N > p.Trans[j].N })

	for i := len(sum.Games) - 1; i >= 0 && len(p.Games) < 40; i-- {
		g := sum.Games[i]
		pg := protoGame{ID: g.MatchID, Champ: champName(g.Champion), Icon: champImg(g.Champion, g.Patch), Win: g.Win,
			K: g.Kills, D: g.Deaths, A: g.Assists, Date: g.Start.Format("Jan 2"), Role: roleKey(g.Role)}
		if g.At15 != nil && g.At15.HasOpp {
			v := g.At15.GoldDiff
			pg.GD15 = &v
		}
		p.Games = append(p.Games, pg)
	}

	b, _ := json.Marshal(p)
	pd := protoData{overviewData: d, Variant: variant, Variants: protoVariants, JSON: string(b), Champs: p.Champs, Deaths: len(p.Deaths)}
	if len(p.Champs) > 0 {
		pd.Top = &p.Champs[0]
	}
	if d.Rank != nil {
		pd.RankList = []acctRank{{RiotID: d.RiotID, Rank: d.Rank}}
	} else {
		pd.RankList = d.Ranks
	}
	for i, g := range p.Games {
		if i >= 20 {
			break
		}
		pd.Recent20 = append(pd.Recent20, g)
		if g.Win {
			pd.Wins20++
		}
	}
	s.render(w, r, "proto_overview", "", pd)
}
