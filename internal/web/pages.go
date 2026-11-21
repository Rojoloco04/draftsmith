package web

import (
	"errors"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"slices"
	"sort"
	"strconv"

	"github.com/rojoloco04/draftsmith/internal/analysis"
	"github.com/rojoloco04/draftsmith/internal/db"
	"github.com/rojoloco04/draftsmith/internal/meta"
)

const pageSize = 20

// loadSummary reads every game in the selected queue and summarizes them.
func (s *Server) loadSummary(r *http.Request, f db.GameFilter) ([]analysis.Game, analysis.Summary, error) {
	f.Queue = queueFrom(r).ID
	f.Account = s.puuid(r)
	games, err := s.db.Games(r.Context(), f)
	if err != nil {
		return nil, analysis.Summary{}, err
	}
	return games, analysis.Summarize(games, 1), nil
}

// latestPatch is the newest patch in the summary, used for icon versions.
func latestPatch(sum analysis.Summary) string {
	if len(sum.Games) == 0 {
		return ""
	}
	return sum.Games[len(sum.Games)-1].Patch
}

// champRows groups games by champion with adjusted win rates against base,
// most played first.
func champRows(games []analysis.Game, base float64) []analysis.Group {
	rows := analysis.GroupBy(games, func(g analysis.Game) string { return g.Champion })
	for i := range rows {
		rows[i].Adjusted = rows[i].AdjustedWR(base)
	}
	analysis.SortByGames(rows)
	return rows
}

func rankQueue(q queueOpt) string {
	if q.Key == "flex" {
		return "RANKED_FLEX_SR"
	}
	return "RANKED_SOLO_5x5"
}

type overviewData struct {
	Base
	Sum   analysis.Summary
	Patch string
	// Rank is the selected account's latest snapshot. With all accounts
	// selected it's nil, and Ranks has each account's instead.
	Rank     *db.RankSnapshot
	Ranks    []acctRank
	RankDays int
	RankFrom string
	LPChart  template.HTML
	// Splash is the most played champion, for the banner's art.
	Splash string
	Recent []analysis.Game
	Pool   template.HTML
}

// recentGames is how many games the overview's strip shows.
const recentGames = 20

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	_, sum, err := s.loadSummary(r, db.GameFilter{})
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err)
		return
	}
	d := overviewData{Base: s.base(r, "Overview", "overview"), Sum: sum, Patch: latestPatch(sum)}

	accts := s.accounts
	if d.Account != nil {
		accts = []Account{*d.Account}
	}
	var hists []rankHist
	for _, a := range accts {
		hist, err := s.db.RankHistory(r.Context(), a.PUUID, rankQueue(d.Queue))
		if err != nil {
			s.fail(w, r, http.StatusInternalServerError, err)
			return
		}
		if len(hist) == 0 {
			continue
		}
		d.Ranks = append(d.Ranks, acctRank{a.RiotID, &hist[len(hist)-1], seriesClasses[len(hists)%len(seriesClasses)]})
		hists = append(hists, rankHist{a.RiotID, hist})
		d.RankDays = max(d.RankDays, len(hist))
		if d.RankFrom == "" || hist[0].Day < d.RankFrom {
			d.RankFrom = hist[0].Day
		}
	}
	if d.Account != nil && len(d.Ranks) == 1 {
		d.Rank, d.Ranks = d.Ranks[0].Rank, nil
	}
	d.LPChart = lpChart(hists)

	champs := champRows(sum.Games, sum.Base)
	if len(champs) > 0 {
		d.Splash = champs[0].Key
	}
	d.Pool = poolPlot(champs, sum.Base, d.Patch, d.Queue.Key)
	for i := len(sum.Games) - 1; i >= 0 && len(d.Recent) < recentGames; i-- {
		d.Recent = append(d.Recent, sum.Games[i])
	}
	s.render(w, r, "overview", "", d)
}

// acctRank is one account's latest standing.
type acctRank struct {
	RiotID string
	Rank   *db.RankSnapshot
	Class  string // its line's color in the rank chart
}

// rankHist is one account's rank history.
type rankHist struct {
	RiotID string
	Hist   []db.RankSnapshot
}

// seriesClasses color the accounts' lines in order (style.css).
var seriesClasses = []string{"s1", "s3", "s2"}

// lpChart draws each account's rank over the days any of them has a
// snapshot for, one line per account.
func lpChart(hists []rankHist) template.HTML {
	var days []string
	for _, h := range hists {
		for _, s := range h.Hist {
			days = append(days, s.Day)
		}
	}
	slices.Sort(days)
	days = slices.Compact(days)
	at := map[string]int{}
	for i, d := range days {
		at[d] = i
	}

	var series []Series
	lo, hi := math.Inf(1), math.Inf(-1)
	for i, h := range hists {
		s := Series{Name: h.RiotID, Class: seriesClasses[i%len(seriesClasses)], Dots: true}
		who := ""
		if len(hists) > 1 {
			who = h.RiotID + " "
		}
		for _, r := range h.Hist {
			v := ladderPoints(r.Tier, r.Division, r.LP)
			if math.IsNaN(v) {
				continue
			}
			lo, hi = math.Min(lo, v), math.Max(hi, v)
			s.Points = append(s.Points, Point{X: float64(at[r.Day]), Y: v,
				Label: fmt.Sprintf("%s%s: %s %s %d LP (%dW %dL)", who, r.Day, r.Tier, r.Division, r.LP, r.Wins, r.Losses)})
		}
		if len(s.Points) > 0 {
			series = append(series, s)
		}
	}
	if len(series) == 0 {
		return ""
	}
	// Show at least the surrounding division so one day isn't a flat line.
	lo = math.Floor(lo/100) * 100
	hi = math.Max(math.Ceil(hi/100)*100, lo+100)
	var ticks []float64
	if len(days) > 1 {
		ticks = []float64{0, float64(len(days) - 1)}
	} else {
		ticks = []float64{0}
	}
	return LineChart{
		Title:   "Rank history",
		Width:   1160, // full width on the overview
		Series:  series,
		YRange:  &[2]float64{lo, hi},
		YTicks:  int((hi - lo) / 100),
		YFormat: ladderLabel,
		XTicks:  ticks,
		XFormat: func(x float64) string {
			i := int(x)
			if i >= 0 && i < len(days) {
				return days[i]
			}
			return ""
		},
	}.SVG()
}

func winRateChart(games []analysis.Game) template.HTML {
	if len(games) < 10 {
		return ""
	}
	var series []Series
	for _, w := range []struct {
		n     int
		class string
	}{{10, "s2"}, {20, "s1"}} {
		roll := analysis.RollingWinRate(games, w.n)
		if roll == nil {
			continue
		}
		s := Series{Name: fmt.Sprintf("Last %d games", w.n), Class: w.class}
		for i, v := range roll {
			gi := i + w.n - 1
			s.Points = append(s.Points, Point{X: float64(gi + 1), Y: v,
				Label: fmt.Sprintf("Game %d (%s): %s over the last %d", gi+1, games[gi].Start.Format("Jan 2"), analysis.Pct(v), w.n)})
		}
		series = append(series, s)
	}
	return LineChart{
		Title:   "Rolling win rate",
		Series:  series,
		YRange:  &[2]float64{0, 1},
		RefY:    []float64{0.5},
		YFormat: func(v float64) string { return analysis.Pct(v) },
		XFormat: func(v float64) string { return "game " + strconv.Itoa(int(v)) },
		XTicks:  []float64{10, float64(len(games))},
	}.SVG()
}

// matchRow is a game in a match list, with my items, spells and runes
// when the index has them.
type matchRow struct {
	analysis.Game
	Line    analysis.PlayerLine
	HasLine bool
}

func (s *Server) matchRows(r *http.Request, games []analysis.Game) ([]matchRow, error) {
	ids := make([]string, len(games))
	for i, g := range games {
		ids[i] = g.MatchID
	}
	lines, err := s.db.MyLines(r.Context(), ids)
	if err != nil {
		return nil, err
	}
	rows := make([]matchRow, len(games))
	for i, g := range games {
		rows[i].Game = g
		rows[i].Line, rows[i].HasLine = lines[g.MatchID]
	}
	return rows, nil
}

type matchesData struct {
	Base
	Games      []matchRow
	Total      int
	Page       int
	Pages      int
	Role       string
	Champion   string
	Champions  []string
	Roles      []string
	PrevQuery  string
	NextQuery  string
	FilterNote string
}

func (s *Server) matches(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	d := matchesData{Base: s.base(r, "Match history", "matches"), Role: q.Get("role"), Champion: q.Get("champion"), Roles: analysis.Roles}
	page, _ := strconv.Atoi(q.Get("page"))
	d.Page = max(page, 1)

	all, err := s.db.Games(r.Context(), db.GameFilter{Account: s.puuid(r), Queue: d.Queue.ID})
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err)
		return
	}
	seen := map[string]bool{}
	for _, g := range all {
		if !seen[g.Champion] {
			seen[g.Champion] = true
			d.Champions = append(d.Champions, g.Champion)
		}
	}
	sort.Slice(d.Champions, func(i, j int) bool { return champName(d.Champions[i]) < champName(d.Champions[j]) })

	f := db.GameFilter{Account: s.puuid(r), Queue: d.Queue.ID, Role: d.Role, Champion: d.Champion}
	games, total, err := s.db.GamePage(r.Context(), f, (d.Page-1)*pageSize, pageSize)
	if err == nil {
		d.Total = total
		d.Games, err = s.matchRows(r, games)
	}
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err)
		return
	}
	d.Pages = max(1, (d.Total+pageSize-1)/pageSize)
	link := func(p int) string {
		v := q
		v.Set("page", strconv.Itoa(p))
		return v.Encode()
	}
	if d.Page > 1 {
		d.PrevQuery = link(d.Page - 1)
	}
	if d.Page < d.Pages {
		d.NextQuery = link(d.Page + 1)
	}
	block := ""
	if isPartial(r) {
		block = "results"
	}
	s.render(w, r, "matches", block, d)
}

type matchData struct {
	Base
	Game      analysis.Game
	Me        analysis.PlayerLine
	Opp       *analysis.PlayerLine // my lane opponent, if known
	Teams     []team
	MaxDamage int
	GoldChart template.HTML
	CSChart   template.HTML
	DeathMap  template.HTML
}

type team struct {
	Name    string
	Side    string // blue, red
	Win     bool
	Mine    bool
	Kills   int
	Gold    int
	Players []analysis.PlayerLine
}

func (s *Server) match(w http.ResponseWriter, r *http.Request) {
	g, det, err := s.db.Match(r.Context(), r.PathValue("id"))
	if errors.Is(err, db.ErrNotFound) {
		s.fail(w, r, http.StatusNotFound, fmt.Errorf("no indexed match %q", r.PathValue("id")))
		return
	}
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err)
		return
	}
	d := matchData{Base: s.base(r, champName(g.Champion)+" "+g.Start.Format("Jan 2"), "matches"), Game: g}

	blue := team{Name: "Blue side", Side: "blue"}
	red := team{Name: "Red side", Side: "red"}
	for _, p := range det.Players {
		t := &blue
		if p.TeamID == 200 {
			t = &red
		}
		t.Players = append(t.Players, p)
		t.Win = p.Win
		t.Kills += p.Kills
		t.Gold += p.Gold
		if p.Me {
			t.Mine = true
			d.Me = p
		}
		d.MaxDamage = max(d.MaxDamage, p.Damage)
	}
	for i, p := range det.Players {
		if g.Opponent != "" && p.Champion == g.Opponent && p.TeamID != d.Me.TeamID && p.Position == d.Me.Position {
			d.Opp = &det.Players[i]
		}
	}
	d.Teams = []team{blue, red}
	if red.Mine { // my team first
		d.Teams = []team{red, blue}
	}

	if len(det.Lane) > 1 && !g.Remake { // a remake's minute or two says nothing
		var me, opp, meCS, oppCS Series
		me = Series{Name: "My gold", Class: "s1"}
		opp = Series{Name: "Opponent gold", Class: "s3"}
		meCS = Series{Name: "My CS", Class: "s1"}
		oppCS = Series{Name: "Opponent CS", Class: "s3"}
		for _, f := range det.Lane {
			m := fmt.Sprintf("%.0f:%02.0f", math.Floor(f.Minute), math.Mod(f.Minute*60, 60))
			me.Points = append(me.Points, Point{X: f.Minute, Y: float64(f.Gold), Label: fmt.Sprintf("%s  me %d gold", m, f.Gold)})
			meCS.Points = append(meCS.Points, Point{X: f.Minute, Y: float64(f.CS), Label: fmt.Sprintf("%s  me %d CS", m, f.CS)})
			if f.HasOpp {
				opp.Points = append(opp.Points, Point{X: f.Minute, Y: float64(f.OppGold),
					Label: fmt.Sprintf("%s  %s %d gold (%+d)", m, champName(g.Opponent), f.OppGold, f.Gold-f.OppGold)})
				oppCS.Points = append(oppCS.Points, Point{X: f.Minute, Y: float64(f.OppCS),
					Label: fmt.Sprintf("%s  %s %d CS (%+d)", m, champName(g.Opponent), f.OppCS, f.CS-f.OppCS)})
			}
		}
		last := det.Lane[len(det.Lane)-1].Minute
		var xt []float64
		for m := 0.0; m <= last; m += 5 {
			xt = append(xt, m)
		}
		mins := func(v float64) string { return fmt.Sprintf("%.0fm", v) }
		d.GoldChart = LineChart{Title: "Gold over time", Series: []Series{opp, me}, XTicks: xt, XFormat: mins,
			YFormat: goldTick}.SVG()
		d.CSChart = LineChart{Title: "CS over time", Series: []Series{oppCS, meCS}, XTicks: xt, XFormat: mins}.SVG()
	}
	if g.HasTimeline {
		var pts []Point
		for _, dth := range g.DeathSpots {
			pts = append(pts, Point{X: float64(dth.X), Y: float64(dth.Y), Label: duration(dth.Minute)})
		}
		d.DeathMap = DeathMap(pts, "Where I died")
	}
	s.render(w, r, "match", "", d)
}

type championsData struct {
	Base
	Sum   analysis.Summary
	Rows  []analysis.Group
	Sort  string
	Patch string
	// Fits compares the pool with OP.GG's tier list for champions with
	// meta.PoolMinGames or more; FitBy has every champion, for the table.
	Fits    []meta.Fit
	FitBy   map[string]meta.Fit
	Meta    meta.TierList
	MetaErr error
	// RoleMax is the most games in one role, for the roles card's bars.
	RoleMax int
}

var champSorts = map[string]func(a, b analysis.Group) bool{
	"games": func(a, b analysis.Group) bool { return a.Games > b.Games },
	"adj":   func(a, b analysis.Group) bool { return a.Adjusted > b.Adjusted },
	"wr":    func(a, b analysis.Group) bool { return a.WinRate() > b.WinRate() },
	"kda":   func(a, b analysis.Group) bool { return a.KDA() > b.KDA() },
	"cs":    func(a, b analysis.Group) bool { return a.CSPerMin() > b.CSPerMin() },
	"gd15":  func(a, b analysis.Group) bool { return nanLast(a.GoldDiff15(), b.GoldDiff15()) },
}

// nanLast orders descending with NaN at the end.
func nanLast(a, b float64) bool {
	if math.IsNaN(b) {
		return !math.IsNaN(a)
	}
	return !math.IsNaN(a) && a > b
}

func (s *Server) champions(w http.ResponseWriter, r *http.Request) {
	_, sum, err := s.loadSummary(r, db.GameFilter{})
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err)
		return
	}
	d := championsData{Base: s.base(r, "Champions", "champions"), Sum: sum, Patch: latestPatch(sum), Sort: r.URL.Query().Get("sort")}
	less, ok := champSorts[d.Sort]
	if !ok {
		d.Sort, less = "games", champSorts["games"]
	}
	d.Rows = champRows(sum.Games, sum.Base)
	for _, g := range sum.Roles {
		d.RoleMax = max(d.RoleMax, g.Games)
	}
	sort.SliceStable(d.Rows, func(i, j int) bool { return less(d.Rows[i], d.Rows[j]) })
	var fits []meta.Fit
	fits, d.Meta, d.MetaErr = s.poolFit(r, sum)
	d.FitBy = map[string]meta.Fit{}
	for _, f := range fits {
		d.FitBy[f.Champion] = f
		if f.Games >= meta.PoolMinGames {
			d.Fits = append(d.Fits, f)
		}
	}
	s.render(w, r, "champions", "", d)
}

type championData struct {
	Base
	Name     string
	Patch    string
	Overall  float64
	Stats    analysis.Group
	Roles    []analysis.Group
	Matchups []analysis.Group
	Games    []matchRow
	WRChart  template.HTML
	Meta     championMeta
}

func (s *Server) champion(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	_, sum, err := s.loadSummary(r, db.GameFilter{})
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err)
		return
	}
	var games []analysis.Game
	for _, g := range sum.Games {
		if g.Champion == name {
			games = append(games, g)
		}
	}
	if len(games) == 0 {
		// Played in another queue or on another account: keep the page,
		// with its queue switch, rather than a dead end.
		other, err := s.db.Games(r.Context(), db.GameFilter{Champion: name})
		if err != nil {
			s.fail(w, r, http.StatusInternalServerError, err)
			return
		}
		if len(other) == 0 {
			s.fail(w, r, http.StatusNotFound, fmt.Errorf("no %s games in the archive", champName(name)))
			return
		}
		s.render(w, r, "champion", "", championData{Base: s.base(r, champName(name), "champions"), Name: name,
			Overall: sum.Base, Stats: analysis.Group{Key: name}, Patch: other[len(other)-1].Patch})
		return
	}
	d := championData{Base: s.base(r, champName(name), "champions"), Name: name, Overall: sum.Base, Patch: games[len(games)-1].Patch}
	d.Stats = analysis.Group{Key: name}
	for _, g := range games {
		d.Stats.Add(g)
	}
	d.Stats.Adjusted = d.Stats.AdjustedWR(sum.Base)
	d.Roles = analysis.GroupBy(games, func(g analysis.Game) string { return analysis.RoleName(g.Role) })
	analysis.SortByGames(d.Roles)
	d.Matchups = analysis.Matchups(games, "", sum.Base, 1)
	analysis.SortByGames(d.Matchups)
	newest := make([]analysis.Game, 0, len(games))
	for i := len(games) - 1; i >= 0; i-- {
		newest = append(newest, games[i])
	}
	if d.Games, err = s.matchRows(r, newest); err != nil {
		s.fail(w, r, http.StatusInternalServerError, err)
		return
	}
	d.WRChart = winRateChart(games)
	d.Meta = s.championMeta(r, name, d.Patch, games)
	s.render(w, r, "champion", "", d)
}

// goldTick labels a gold axis: 0, 500, 5k, 12.5k.
func goldTick(v float64) string {
	if math.Abs(v) < 1000 {
		return fmt.Sprintf("%g", v)
	}
	return fmt.Sprintf("%gk", v/1000)
}
