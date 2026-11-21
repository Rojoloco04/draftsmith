package analysis

import (
	"math"
	"sort"
)

// DS score: how well each player in a match did, 0 to 10, judged against
// the other nine. Each stat below is turned into a z-score across the
// lobby (how many standard deviations above or below the lobby's
// average), the z-scores are summed with role weights, the winning team
// gets a small bump, and the total maps to 0-10 around 5. The best
// player on the winning team is the MVP, the best on the losing team the
// SVP.

// dsStat is one input to the score, computed from a player's line.
type dsStat int

const (
	dsKDA        dsStat = iota // log(1 + (kills + assists) / max(1, deaths))
	dsKP                       // kill participation
	dsDamage                   // share of the team's champion damage
	dsGold                     // gold per minute
	dsLaneGold                 // gold lead over the lane opponent per minute
	dsCS                       // CS per minute
	dsVision                   // vision score per minute
	dsDeaths                   // deaths per minute (weighted negative)
	dsStatCount
)

// dsWeights by Riot position. Supports aren't judged on CS and junglers
// less so; supports lean on vision and kill participation instead.
var dsWeights = map[string][dsStatCount]float64{
	"":        {.22, .15, .16, .10, .12, .10, .07, -.08},
	"TOP":     {.22, .13, .17, .10, .14, .12, .05, -.08},
	"JUNGLE":  {.22, .18, .14, .10, .10, .07, .10, -.08},
	"MIDDLE":  {.22, .15, .17, .10, .13, .11, .05, -.08},
	"BOTTOM":  {.22, .14, .18, .10, .13, .12, .04, -.08},
	"UTILITY": {.22, .22, .10, .06, .08, 0, .22, -.08},
}

const (
	dsWinBonus = .25 // in z units, added for the winning team
	dsSpread   = 1.6 // score points per z unit
)

// Awards.
const (
	AwardMVP = "MVP" // best on the winning team
	AwardSVP = "SVP" // best on the losing team
)

// ScorePlayers sets DSScore, DSRank (1 is best) and Award on every player
// of one match. minutes is the game length; remakes get no score.
func ScorePlayers(ps []PlayerLine, minutes float64, remake bool) {
	for i := range ps {
		ps[i].DSScore, ps[i].DSRank, ps[i].Award = 0, 0, ""
	}
	if remake || minutes <= 0 || len(ps) < 2 {
		return
	}
	teamKills, teamDamage := map[int]int{}, map[int]int{}
	for _, p := range ps {
		teamKills[p.TeamID] += p.Kills
		teamDamage[p.TeamID] += p.Damage
	}
	stats := make([][dsStatCount]float64, len(ps))
	for i, p := range ps {
		s := &stats[i]
		s[dsKDA] = math.Log1p(float64(p.Kills+p.Assists) / math.Max(1, float64(p.Deaths)))
		s[dsKP] = share(p.Kills+p.Assists, teamKills[p.TeamID])
		s[dsDamage] = share(p.Damage, teamDamage[p.TeamID])
		s[dsGold] = float64(p.Gold) / minutes
		s[dsCS] = float64(p.CS) / minutes
		s[dsVision] = float64(p.Vision) / minutes
		s[dsDeaths] = float64(p.Deaths) / minutes
		for _, o := range ps {
			if o.TeamID != p.TeamID && o.Position == p.Position && p.Position != "" {
				s[dsLaneGold] = float64(p.Gold-o.Gold) / minutes
				break
			}
		}
	}
	var z [dsStatCount][]float64
	for k := dsStat(0); k < dsStatCount; k++ {
		col := make([]float64, len(ps))
		for i := range ps {
			col[i] = stats[i][k]
		}
		z[k] = zscores(col)
	}
	raw := make([]float64, len(ps))
	for i, p := range ps {
		w, ok := dsWeights[p.Position]
		if !ok {
			w = dsWeights[""]
		}
		for k := dsStat(0); k < dsStatCount; k++ {
			raw[i] += w[k] * z[k][i]
		}
		if p.Win {
			raw[i] += dsWinBonus
		}
		ps[i].DSScore = math.Round(math.Max(0, math.Min(10, 5+dsSpread*raw[i]))*10) / 10
	}

	order := make([]int, len(ps))
	for i := range order {
		order[i] = i
	}
	// Rank by the unrounded score; ties (rare) go to more kills + assists.
	sort.SliceStable(order, func(a, b int) bool {
		i, j := order[a], order[b]
		if raw[i] != raw[j] {
			return raw[i] > raw[j]
		}
		return ps[i].Kills+ps[i].Assists > ps[j].Kills+ps[j].Assists
	})
	mvp, svp := false, false
	for rank, i := range order {
		ps[i].DSRank = rank + 1
		switch {
		case ps[i].Win && !mvp:
			ps[i].Award, mvp = AwardMVP, true
		case !ps[i].Win && !svp:
			ps[i].Award, svp = AwardSVP, true
		}
	}
}

// share is x / total, 0 when the total is 0.
func share(x, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(x) / float64(total)
}

// zscores standardizes xs; all zeros when they don't vary.
func zscores(xs []float64) []float64 {
	var mean float64
	for _, x := range xs {
		mean += x
	}
	mean /= float64(len(xs))
	var v float64
	for _, x := range xs {
		v += (x - mean) * (x - mean)
	}
	sd := math.Sqrt(v / float64(len(xs)))
	out := make([]float64, len(xs))
	if sd < 1e-9 {
		return out
	}
	for i, x := range xs {
		out[i] = (x - mean) / sd
	}
	return out
}
