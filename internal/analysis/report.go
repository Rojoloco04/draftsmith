package analysis

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
)

// PriorWeight is how many games of "average you" a champion's record is
// blended with before ranking. 15 games on a champ is mostly noise, so raw
// win rate would mislead.
const PriorWeight = 10.0

type ReportInput struct {
	RiotID   string
	Rank     string // e.g. "EMERALD II 45 LP (120W 110L)"
	RankDate string
	Games    []Game
	// MinGames hides champions and matchups with fewer games.
	MinGames int
}

// agg accumulates averages over a set of games.
type agg struct {
	n, wins                     int
	k, d, a                     int
	csm, gpm, vpm, kp, dmg, cw  float64
	gd15, csd10, xpd15          float64
	nLane10, nLane15            int
	early, enemySide, nTimeline int
}

func (a *agg) add(g Game) {
	a.n++
	if g.Win {
		a.wins++
	}
	a.k += g.Kills
	a.d += g.Deaths
	a.a += g.Assists
	a.csm += g.CSPerMin
	a.gpm += g.GoldPerMin
	a.vpm += g.VisionPerMin
	a.kp += g.KillParticipation
	a.dmg += g.DamageShare
	a.cw += float64(g.ControlWards)
	if g.At10 != nil && g.At10.HasOpp {
		a.nLane10++
		a.csd10 += float64(g.At10.CSDiff)
	}
	if g.At15 != nil && g.At15.HasOpp {
		a.nLane15++
		a.gd15 += float64(g.At15.GoldDiff)
		a.xpd15 += float64(g.At15.XPDiff)
	}
	if g.HasTimeline {
		a.nTimeline++
		a.early += g.DeathsBefore15
		a.enemySide += g.DeathsEnemySide
	}
}

func (a agg) wr() float64 { return ratio(float64(a.wins), float64(a.n)) }
func (a agg) kda() float64 {
	return float64(a.k+a.a) / math.Max(1, float64(a.d))
}
func (a agg) avg(sum float64) float64 { return ratio(sum, float64(a.n)) }

// shrunkWR blends the record with prior games at the baseline win rate.
func (a agg) shrunkWR(base float64) float64 {
	return (float64(a.wins) + PriorWeight*base) / (float64(a.n) + PriorWeight)
}

func ratio(x, y float64) float64 {
	if y == 0 {
		return math.NaN()
	}
	return x / y
}

var roleNames = map[string]string{
	"TOP": "Top", "JUNGLE": "Jungle", "MIDDLE": "Mid", "BOTTOM": "Bot", "UTILITY": "Support",
}

func roleName(r string) string {
	if n, ok := roleNames[r]; ok {
		return n
	}
	if r == "" || r == "Invalid" {
		return "Unknown"
	}
	return r
}

// WriteReport renders a markdown report.
func WriteReport(w io.Writer, in ReportInput) {
	if in.MinGames <= 0 {
		in.MinGames = 5
	}
	var games []Game
	remakes := 0
	for _, g := range in.Games {
		if g.Remake {
			remakes++
			continue
		}
		games = append(games, g)
	}

	p := func(format string, a ...any) { fmt.Fprintf(w, format+"\n", a...) }

	p("# Ranked report: %s", in.RiotID)
	p("")
	if len(games) == 0 {
		p("No ranked games archived yet. Run `lolstats sync` first.")
		return
	}
	var all agg
	timelines := 0
	for _, g := range games {
		all.add(g)
		if g.HasTimeline {
			timelines++
		}
	}
	base := all.wr()

	p("%d games from %s to %s (%d remakes skipped, %d with timelines).",
		len(games), games[0].Start.Format("Jan 2 2006"), games[len(games)-1].Start.Format("Jan 2 2006"), remakes, timelines)
	if in.Rank != "" {
		p("Current rank (as of %s): **%s**", in.RankDate, in.Rank)
	}
	p("")

	// Seasons
	p("## By season")
	p("")
	p("| Season | Games | Win rate | KDA | CS/min | Vision/min |")
	p("|---|---:|---:|---:|---:|---:|")
	seasons := groupBy(games, func(g Game) string { return g.Start.Format("2006") })
	for _, k := range sortedKeys(seasons) {
		a := seasons[k]
		p("| %s | %d | %s | %.2f | %.1f | %.2f |", k, a.n, pct(a.wr()), a.kda(), a.avg(a.csm), a.avg(a.vpm))
	}
	p("")
	p("Seasons are calendar years, which matches how Riot splits seasons since 2025. Riot deletes match data after about two years, so older seasons only show up if they were archived in time.")
	p("")

	// Roles
	p("## By role")
	p("")
	p("| Role | Games | Win rate | CS/min | Vision/min | Ctrl wards | KP | Dmg share | CS diff @10 | Gold diff @15 | Deaths <15m |")
	p("|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|")
	roles := groupBy(games, func(g Game) string { return roleName(g.Role) })
	for _, k := range sortedByCount(roles) {
		a := roles[k]
		p("| %s | %d | %s | %.1f | %.2f | %.1f | %s | %s | %s | %s | %s |",
			k, a.n, pct(a.wr()), a.avg(a.csm), a.avg(a.vpm), a.avg(a.cw), pct(a.avg(a.kp)), pct(a.avg(a.dmg)),
			signed(ratio(a.csd10, float64(a.nLane10)), 1), signed(ratio(a.gd15, float64(a.nLane15)), 0),
			num(ratio(float64(a.early), float64(a.nTimeline)), 2))
	}
	p("")

	// Champion pool
	p("## Champion pool")
	p("")
	p("Adjusted win rate blends each champion's record with %d games at your overall win rate (%s), so small samples don't look better or worse than they are. Champions with fewer than %d games are hidden.", int(PriorWeight), pct(base), in.MinGames)
	p("")
	p("| Champion | Games | Win rate | Adjusted | KDA | CS/min | Gold diff @15 | Read |")
	p("|---|---:|---:|---:|---:|---:|---:|---|")
	champs := groupBy(games, func(g Game) string { return g.Champion })
	type champRow struct {
		name string
		a    agg
		adj  float64
	}
	var rows []champRow
	for name, a := range champs {
		if a.n >= in.MinGames {
			rows = append(rows, champRow{name, a, a.shrunkWR(base)})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].adj > rows[j].adj })
	var strengths, drains []string
	for _, r := range rows {
		read := ""
		switch {
		case r.adj >= base+0.03 && r.a.n >= 10:
			read = "strength"
			strengths = append(strengths, r.name)
		case r.adj <= base-0.03 && r.a.n >= 10:
			read = "drain"
			drains = append(drains, r.name)
		}
		p("| %s | %d | %s | %s | %.2f | %.1f | %s | %s |", r.name, r.a.n, pct(r.a.wr()), pct(r.adj), r.a.kda(), r.a.avg(r.a.csm),
			signed(ratio(r.a.gd15, float64(r.a.nLane15)), 0), read)
	}
	p("")

	// Early game vs outcome
	p("## Early game vs result")
	p("")
	p("Win rate by your gold lead over your lane opponent at 15 minutes.")
	p("")
	p("| Gold diff @15 | Games | Win rate |")
	p("|---|---:|---:|")
	buckets := []struct {
		label  string
		lo, hi int
	}{
		{"behind 1000+", math.MinInt32, -1000},
		{"behind 300-1000", -1000, -300},
		{"even (within 300)", -300, 301},
		{"ahead 300-1000", 301, 1001},
		{"ahead 1000+", 1001, math.MaxInt32},
	}
	bucketAgg := make([]agg, len(buckets))
	for _, g := range games {
		if g.At15 == nil || !g.At15.HasOpp {
			continue
		}
		for i, b := range buckets {
			if g.At15.GoldDiff >= b.lo && g.At15.GoldDiff < b.hi {
				bucketAgg[i].add(g)
			}
		}
	}
	for i, b := range buckets {
		p("| %s | %d | %s |", b.label, bucketAgg[i].n, pct(bucketAgg[i].wr()))
	}
	p("")

	// Matchups in main role
	mainRole := ""
	if keys := sortedByCount(roles); len(keys) > 0 {
		mainRole = keys[0]
	}
	if mainRole != "" {
		var inRole []Game
		for _, g := range games {
			if roleName(g.Role) == mainRole && g.Opponent != "" {
				inRole = append(inRole, g)
			}
		}
		opps := groupBy(inRole, func(g Game) string { return g.Opponent })
		type oppRow struct {
			name string
			a    agg
			adj  float64
		}
		var orows []oppRow
		for name, a := range opps {
			if a.n >= 3 {
				orows = append(orows, oppRow{name, a, a.shrunkWR(base)})
			}
		}
		sort.Slice(orows, func(i, j int) bool { return orows[i].adj < orows[j].adj })
		if len(orows) > 0 {
			p("## Toughest matchups (%s)", mainRole)
			p("")
			p("Lane opponents you've faced at least 3 times, worst adjusted win rate first.")
			p("")
			p("| Opponent | Games | Win rate | CS diff @10 | Gold diff @15 |")
			p("|---|---:|---:|---:|---:|")
			for i, r := range orows {
				if i == 8 {
					break
				}
				p("| %s | %d | %s | %s | %s |", r.name, r.a.n, pct(r.a.wr()),
					signed(ratio(r.a.csd10, float64(r.a.nLane10)), 1), signed(ratio(r.a.gd15, float64(r.a.nLane15)), 0))
			}
			p("")
		}
	}

	// Recent form
	if len(games) >= 40 {
		var recent, before agg
		for i, g := range games {
			if i >= len(games)-20 {
				recent.add(g)
			} else {
				before.add(g)
			}
		}
		p("## Last 20 games vs everything before")
		p("")
		p("| | Win rate | KDA | CS/min | CS diff @10 | Gold diff @15 | Deaths <15m |")
		p("|---|---:|---:|---:|---:|---:|---:|")
		for _, row := range []struct {
			l string
			a agg
		}{{"Last 20", recent}, {"Before", before}} {
			a := row.a
			p("| %s | %s | %.2f | %.1f | %s | %s | %s |", row.l, pct(a.wr()), a.kda(), a.avg(a.csm),
				signed(ratio(a.csd10, float64(a.nLane10)), 1), signed(ratio(a.gd15, float64(a.nLane15)), 0),
				num(ratio(float64(a.early), float64(a.nTimeline)), 2))
		}
		p("")
	}

	// Observations
	p("## What stands out")
	p("")
	notes := observations(games, bucketAgg, strengths, drains)
	if len(notes) == 0 {
		p("Nothing clear yet. More games, or games with timelines, will help.")
	}
	for _, n := range notes {
		p("- %s", n)
	}
	p("")
	p("These compare you against yourself. Benchmarks against other players in your elo need the `sample` crawler (not built yet).")
}

func observations(games []Game, buckets []agg, strengths, drains []string) []string {
	var out []string

	if ahead := buckets[4]; ahead.n >= 5 {
		lost := ahead.n - ahead.wins
		if ratio(float64(lost), float64(ahead.n)) >= 0.4 {
			out = append(out, fmt.Sprintf("You lost %d of %d games where you were 1000+ gold up on your lane opponent at 15. That's a closing problem: what you do after lane (grouping, side lanes, objective setup), not laning.", lost, ahead.n))
		}
	}
	if behind := buckets[0]; behind.n >= 5 && behind.wr() >= 0.4 {
		out = append(out, fmt.Sprintf("You still win %s of games where you're 1000+ gold down at 15, so falling behind in lane isn't fatal for you. Playing safe when behind is probably working.", pct(behind.wr())))
	}

	var winEarly, lossEarly, lossLaner agg
	for _, g := range games {
		if !g.HasTimeline {
			continue
		}
		if g.Win {
			winEarly.add(g)
		} else {
			lossEarly.add(g)
			if g.Role != "JUNGLE" { // junglers live on the enemy half
				lossLaner.add(g)
			}
		}
	}
	if winEarly.nTimeline >= 10 && lossEarly.nTimeline >= 10 {
		w := ratio(float64(winEarly.early), float64(winEarly.nTimeline))
		l := ratio(float64(lossEarly.early), float64(lossEarly.nTimeline))
		if l-w >= 0.5 {
			out = append(out, fmt.Sprintf("You average %.1f deaths before 15 minutes in losses vs %.1f in wins. Early deaths are a big part of your losses, so look at where they happen (the export includes death positions).", l, w))
		}
	}
	if lossLaner.nTimeline >= 10 {
		es := ratio(float64(lossLaner.enemySide), float64(lossLaner.nTimeline))
		if es >= 2 {
			out = append(out, fmt.Sprintf("In losses you die on the enemy half of the map %.1f times per game. That usually means overextending or face-checking without vision.", es))
		}
	}

	if len(strengths) > 0 {
		out = append(out, "Champions that win more than your baseline even after adjusting for sample size: "+strings.Join(strengths, ", ")+".")
	}
	if len(drains) > 0 {
		out = append(out, "Champions that lose more than your baseline over at least 10 games: "+strings.Join(drains, ", ")+". Worth benching or practicing in normals.")
	}

	champs := groupBy(games, func(g Game) string { return g.Champion })
	if len(games) >= 50 {
		top3 := 0
		for i, k := range sortedByCount(champs) {
			if i == 3 {
				break
			}
			top3 += champs[k].n
		}
		share := float64(top3) / float64(len(games))
		if share < 0.4 {
			out = append(out, fmt.Sprintf("Your top 3 champions are only %s of your games. A wide pool makes it harder to improve on any of them.", pct(share)))
		}
	}
	return out
}

func groupBy(games []Game, key func(Game) string) map[string]agg {
	m := map[string]agg{}
	for _, g := range games {
		k := key(g)
		a := m[k]
		a.add(g)
		m[k] = a
	}
	return m
}

func sortedKeys(m map[string]agg) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedByCount(m map[string]agg) []string {
	keys := sortedKeys(m)
	sort.SliceStable(keys, func(i, j int) bool { return m[keys[i]].n > m[keys[j]].n })
	return keys
}

func pct(x float64) string {
	if math.IsNaN(x) {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", x*100)
}

func signed(x float64, prec int) string {
	if math.IsNaN(x) {
		return "-"
	}
	return fmt.Sprintf("%+.*f", prec, x)
}

func num(x float64, prec int) string {
	if math.IsNaN(x) {
		return "-"
	}
	return fmt.Sprintf("%.*f", prec, x)
}
