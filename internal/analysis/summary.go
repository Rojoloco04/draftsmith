package analysis

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// PriorWeight is how many games of "average you" a champion's record is
// blended with before ranking. 15 games on a champ is mostly noise, so raw
// win rate would mislead.
const PriorWeight = 10.0

// Stats accumulates averages over a set of games. Averages with no data
// (no games, or no games with a lane opponent at the mark) are NaN.
type Stats struct {
	Games, Wins             int
	Kills, Deaths, Assists  int
	csm, gpm, vpm, kp, dmg  float64
	cw                      float64
	gd15, csd10, xpd15      float64
	nLane10, nLane15        int
	early, enemySide, nTime int
}

// Add counts one game.
func (s *Stats) Add(g Game) {
	s.Games++
	if g.Win {
		s.Wins++
	}
	s.Kills += g.Kills
	s.Deaths += g.Deaths
	s.Assists += g.Assists
	s.csm += g.CSPerMin
	s.gpm += g.GoldPerMin
	s.vpm += g.VisionPerMin
	s.kp += g.KillParticipation
	s.dmg += g.DamageShare
	s.cw += float64(g.ControlWards)
	if g.At10 != nil && g.At10.HasOpp {
		s.nLane10++
		s.csd10 += float64(g.At10.CSDiff)
	}
	if g.At15 != nil && g.At15.HasOpp {
		s.nLane15++
		s.gd15 += float64(g.At15.GoldDiff)
		s.xpd15 += float64(g.At15.XPDiff)
	}
	if g.HasTimeline {
		s.nTime++
		s.early += g.DeathsBefore15
		s.enemySide += g.DeathsEnemySide
	}
}

func (s Stats) Losses() int           { return s.Games - s.Wins }
func (s Stats) WinRate() float64      { return ratio(float64(s.Wins), float64(s.Games)) }
func (s Stats) KDA() float64          { return float64(s.Kills+s.Assists) / math.Max(1, float64(s.Deaths)) }
func (s Stats) AvgKills() float64     { return s.avg(float64(s.Kills)) }
func (s Stats) AvgDeaths() float64    { return s.avg(float64(s.Deaths)) }
func (s Stats) AvgAssists() float64   { return s.avg(float64(s.Assists)) }
func (s Stats) CSPerMin() float64     { return s.avg(s.csm) }
func (s Stats) GoldPerMin() float64   { return s.avg(s.gpm) }
func (s Stats) VisionPerMin() float64 { return s.avg(s.vpm) }
func (s Stats) ControlWards() float64 { return s.avg(s.cw) }
func (s Stats) KillPart() float64     { return s.avg(s.kp) }
func (s Stats) DamageShare() float64  { return s.avg(s.dmg) }
func (s Stats) CSDiff10() float64     { return ratio(s.csd10, float64(s.nLane10)) }
func (s Stats) GoldDiff15() float64   { return ratio(s.gd15, float64(s.nLane15)) }
func (s Stats) XPDiff15() float64     { return ratio(s.xpd15, float64(s.nLane15)) }

// DeathsBefore15 and DeathsEnemySide average over games with a timeline.
func (s Stats) DeathsBefore15() float64  { return ratio(float64(s.early), float64(s.nTime)) }
func (s Stats) DeathsEnemySide() float64 { return ratio(float64(s.enemySide), float64(s.nTime)) }

// AdjustedWR blends the record with PriorWeight games at the base win rate.
func (s Stats) AdjustedWR(base float64) float64 {
	return (float64(s.Wins) + PriorWeight*base) / (float64(s.Games) + PriorWeight)
}

func (s Stats) avg(sum float64) float64 { return ratio(sum, float64(s.Games)) }

func ratio(x, y float64) float64 {
	if y == 0 {
		return math.NaN()
	}
	return x / y
}

// Group is the stats for one value of a grouping key (a season, role,
// champion, or lane opponent).
type Group struct {
	Key string
	Stats
	// Adjusted is the shrunk win rate (see PriorWeight); Read is "strength",
	// "drain", or empty. Both are only set where the summary ranks groups.
	Adjusted float64
	Read     string
}

// Bucket is win rate within a range of gold diff at 15.
type Bucket struct {
	Label  string
	Lo, Hi int // Lo <= diff < Hi
	Stats
}

// GoldBuckets are the ranges used for "early game vs result".
var GoldBuckets = []Bucket{
	{Label: "behind 1000+", Lo: math.MinInt32, Hi: -1000},
	{Label: "behind 300-1000", Lo: -1000, Hi: -300},
	{Label: "even (within 300)", Lo: -300, Hi: 301},
	{Label: "ahead 300-1000", Lo: 301, Hi: 1001},
	{Label: "ahead 1000+", Lo: 1001, Hi: math.MaxInt32},
}

// Summary is everything the report and dashboard show, computed once from a
// list of games.
type Summary struct {
	// Games excludes remakes, oldest first.
	Games     []Game
	Remakes   int
	Timelines int
	MinGames  int

	Overall Stats
	// Base is the overall win rate that adjusted win rates shrink toward.
	Base float64

	Seasons []Group // by calendar year, ascending
	Roles   []Group // most played first, Key is the display name
	// Champions with at least MinGames games, best adjusted win rate first.
	Champions []Group
	Buckets   []Bucket

	// MainRole is the most played role (display name) and Matchups the lane
	// opponents faced there at least MatchupMinGames times, worst adjusted
	// win rate first.
	MainRole string
	Matchups []Group

	// Recent and Before split the games into the last 20 and the rest. Only
	// set with at least 40 games.
	Recent, Before *Stats

	Strengths, Drains []string
	Notes             []string
}

// MatchupMinGames hides lane opponents faced fewer times than this.
const MatchupMinGames = 3

// Summarize computes a Summary. games should be oldest first; minGames hides
// champions with fewer games (5 if <= 0).
func Summarize(all []Game, minGames int) Summary {
	if minGames <= 0 {
		minGames = 5
	}
	s := Summary{MinGames: minGames}
	for _, g := range all {
		if g.Remake {
			s.Remakes++
			continue
		}
		s.Games = append(s.Games, g)
		s.Overall.Add(g)
		if g.HasTimeline {
			s.Timelines++
		}
	}
	if len(s.Games) == 0 {
		return s
	}
	games := s.Games
	s.Base = s.Overall.WinRate()

	s.Seasons = GroupBy(games, func(g Game) string { return g.Start.Format("2006") })
	sort.Slice(s.Seasons, func(i, j int) bool { return s.Seasons[i].Key < s.Seasons[j].Key })

	s.Roles = GroupBy(games, func(g Game) string { return RoleName(g.Role) })
	SortByGames(s.Roles)

	for _, c := range GroupBy(games, func(g Game) string { return g.Champion }) {
		if c.Games < minGames {
			continue
		}
		c.Adjusted = c.AdjustedWR(s.Base)
		switch {
		case c.Adjusted >= s.Base+0.03 && c.Games >= 10:
			c.Read = "strength"
		case c.Adjusted <= s.Base-0.03 && c.Games >= 10:
			c.Read = "drain"
		}
		s.Champions = append(s.Champions, c)
	}
	sort.SliceStable(s.Champions, func(i, j int) bool { return s.Champions[i].Adjusted > s.Champions[j].Adjusted })
	for _, c := range s.Champions {
		switch c.Read {
		case "strength":
			s.Strengths = append(s.Strengths, c.Key)
		case "drain":
			s.Drains = append(s.Drains, c.Key)
		}
	}

	s.Buckets = append([]Bucket(nil), GoldBuckets...)
	for _, g := range games {
		if g.At15 == nil || !g.At15.HasOpp {
			continue
		}
		for i, b := range s.Buckets {
			if g.At15.GoldDiff >= b.Lo && g.At15.GoldDiff < b.Hi {
				s.Buckets[i].Add(g)
			}
		}
	}

	if len(s.Roles) > 0 {
		s.MainRole = s.Roles[0].Key
		s.Matchups = Matchups(games, s.MainRole, s.Base, MatchupMinGames)
	}

	if len(games) >= 40 {
		var recent, before Stats
		for i, g := range games {
			if i >= len(games)-20 {
				recent.Add(g)
			} else {
				before.Add(g)
			}
		}
		s.Recent, s.Before = &recent, &before
	}

	s.Notes = observations(games, s.Buckets, s.Strengths, s.Drains)
	return s
}

// Matchups groups games in role (a display name, or "" for every role) by
// lane opponent, keeping opponents faced at least min times, worst adjusted
// win rate first.
func Matchups(games []Game, role string, base float64, min int) []Group {
	var in []Game
	for _, g := range games {
		if (role == "" || RoleName(g.Role) == role) && g.Opponent != "" {
			in = append(in, g)
		}
	}
	var out []Group
	for _, o := range GroupBy(in, func(g Game) string { return g.Opponent }) {
		if o.Games >= min {
			o.Adjusted = o.AdjustedWR(base)
			out = append(out, o)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Adjusted < out[j].Adjusted })
	return out
}

// GroupBy groups games by key, sorted by key.
func GroupBy(games []Game, key func(Game) string) []Group {
	m := map[string]*Stats{}
	for _, g := range games {
		k := key(g)
		if m[k] == nil {
			m[k] = &Stats{}
		}
		m[k].Add(g)
	}
	out := make([]Group, 0, len(m))
	for k, st := range m {
		out = append(out, Group{Key: k, Stats: *st})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// SortByGames orders groups most played first, keeping the existing order
// for ties.
func SortByGames(gs []Group) {
	sort.SliceStable(gs, func(i, j int) bool { return gs[i].Games > gs[j].Games })
}

// RollingWinRate returns, for each game from the window-th on, the win rate
// over the window games ending there. Remakes should already be removed.
func RollingWinRate(games []Game, window int) []float64 {
	if window <= 0 || len(games) < window {
		return nil
	}
	out := make([]float64, 0, len(games)-window+1)
	wins := 0
	for i, g := range games {
		if g.Win {
			wins++
		}
		if i >= window && games[i-window].Win {
			wins--
		}
		if i >= window-1 {
			out = append(out, float64(wins)/float64(window))
		}
	}
	return out
}

var roleNames = map[string]string{
	"TOP": "Top", "JUNGLE": "Jungle", "MIDDLE": "Mid", "BOTTOM": "Bot", "UTILITY": "Support",
}

// RoleName turns a Riot position into a display name.
func RoleName(r string) string {
	if n, ok := roleNames[r]; ok {
		return n
	}
	if r == "" || r == "Invalid" {
		return "Unknown"
	}
	return r
}

// Roles lists Riot positions in lane order.
var Roles = []string{"TOP", "JUNGLE", "MIDDLE", "BOTTOM", "UTILITY"}

func observations(games []Game, buckets []Bucket, strengths, drains []string) []string {
	var out []string

	if ahead := buckets[4]; ahead.Games >= 5 {
		lost := ahead.Losses()
		if ratio(float64(lost), float64(ahead.Games)) >= 0.4 {
			out = append(out, fmt.Sprintf("You lost %d of %d games where you were 1000+ gold up on your lane opponent at 15. That's a closing problem: what you do after lane (grouping, side lanes, objective setup), not laning.", lost, ahead.Games))
		}
	}
	if behind := buckets[0]; behind.Games >= 5 && behind.WinRate() >= 0.4 {
		out = append(out, fmt.Sprintf("You still win %s of games where you're 1000+ gold down at 15, so falling behind in lane isn't fatal for you. Playing safe when behind is probably working.", Pct(behind.WinRate())))
	}

	var winEarly, lossEarly, lossLaner Stats
	for _, g := range games {
		if !g.HasTimeline {
			continue
		}
		if g.Win {
			winEarly.Add(g)
		} else {
			lossEarly.Add(g)
			if g.Role != "JUNGLE" { // junglers live on the enemy half
				lossLaner.Add(g)
			}
		}
	}
	if winEarly.nTime >= 10 && lossEarly.nTime >= 10 {
		w, l := winEarly.DeathsBefore15(), lossEarly.DeathsBefore15()
		if l-w >= 0.5 {
			out = append(out, fmt.Sprintf("You average %.1f deaths before 15 minutes in losses vs %.1f in wins. Early deaths are a big part of your losses, so look at where they happen (the export includes death positions).", l, w))
		}
	}
	if lossLaner.nTime >= 10 {
		if es := lossLaner.DeathsEnemySide(); es >= 2 {
			out = append(out, fmt.Sprintf("In losses you die on the enemy half of the map %.1f times per game. That usually means overextending or face-checking without vision.", es))
		}
	}

	if len(strengths) > 0 {
		out = append(out, "Champions that win more than your baseline even after adjusting for sample size: "+strings.Join(strengths, ", ")+".")
	}
	if len(drains) > 0 {
		out = append(out, "Champions that lose more than your baseline over at least 10 games: "+strings.Join(drains, ", ")+". Worth benching or practicing in normals.")
	}

	if len(games) >= 50 {
		champs := GroupBy(games, func(g Game) string { return g.Champion })
		SortByGames(champs)
		top3 := 0
		for i, c := range champs {
			if i == 3 {
				break
			}
			top3 += c.Games
		}
		share := float64(top3) / float64(len(games))
		if share < 0.4 {
			out = append(out, fmt.Sprintf("Your top 3 champions are only %s of your games. A wide pool makes it harder to improve on any of them.", Pct(share)))
		}
	}
	return out
}

// Pct formats a ratio as a whole percentage, or "-" for NaN.
func Pct(x float64) string {
	if math.IsNaN(x) {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", x*100)
}

// Signed formats x with an explicit sign, or "-" for NaN.
func Signed(x float64, prec int) string {
	if math.IsNaN(x) {
		return "-"
	}
	return fmt.Sprintf("%+.*f", prec, x)
}

// Num formats x, or "-" for NaN.
func Num(x float64, prec int) string {
	if math.IsNaN(x) {
		return "-"
	}
	return fmt.Sprintf("%.*f", prec, x)
}

// span is the date range of the summary's games.
func (s Summary) span() (time.Time, time.Time) {
	return s.Games[0].Start, s.Games[len(s.Games)-1].Start
}
