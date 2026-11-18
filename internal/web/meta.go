package web

import (
	"context"
	"errors"
	"math"
	"net/http"
	"sort"
	"time"

	"github.com/rojoloco04/draftsmith/internal/analysis"
	"github.com/rojoloco04/draftsmith/internal/db"
	"github.com/rojoloco04/draftsmith/internal/meta"
)

// MetaSource provides OP.GG data; *meta.Service implements it.
type MetaSource interface {
	TierList(ctx context.Context) (meta.TierList, error)
	Analysis(ctx context.Context, key, position string) (meta.Analysis, error)
}

// SetMeta turns on the OP.GG side of the dashboard. Call it before Handler.
func (s *Server) SetMeta(m MetaSource) { s.meta = m }

// metaTimeout caps how long a page waits on OP.GG when the cache is cold
// or stale. With a stale copy, a timeout just means the copy is shown.
const metaTimeout = 15 * time.Second

func (s *Server) tierList(ctx context.Context) (meta.TierList, error) {
	if s.meta == nil {
		return meta.TierList{}, errMetaOff
	}
	ctx, cancel := context.WithTimeout(ctx, metaTimeout)
	defer cancel()
	return s.meta.TierList(ctx)
}

var errMetaOff = errors.New("OP.GG data is turned off")

// mainRole is the role with the most games, ties broken in lane order.
func mainRole(games []analysis.Game) string {
	n := map[string]int{}
	for _, g := range games {
		n[g.Role]++
	}
	best := ""
	for _, r := range analysis.Roles {
		if n[r] > n[best] {
			best = r
		}
	}
	return best
}

type positionOpt struct {
	Key, Label string
	Games      int // my games in that role
}

type tierRow struct {
	meta.TierEntry
	Mine    analysis.Stats
	MineAdj float64
}

type tierListData struct {
	Base
	List      meta.TierList
	Err       error
	Position  string
	Positions []positionOpt
	Sort      string
	Rows      []tierRow
	Patch     string
	Overall   float64
}

var tierSorts = map[string]func(a, b tierRow) bool{
	"rank":  func(a, b tierRow) bool { return a.Rank < b.Rank },
	"wr":    func(a, b tierRow) bool { return a.WinRate() > b.WinRate() },
	"games": func(a, b tierRow) bool { return a.Play > b.Play },
	"ban":   func(a, b tierRow) bool { return a.BanRate > b.BanRate },
	"mine":  func(a, b tierRow) bool { return a.Mine.Games > b.Mine.Games },
}

func (s *Server) tierListPage(w http.ResponseWriter, r *http.Request) {
	_, sum, err := s.loadSummary(r, db.GameFilter{})
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err)
		return
	}
	q := r.URL.Query()
	d := tierListData{Base: s.base(r, "Tier list", "tier-list"), Patch: latestPatch(sum), Overall: sum.Base, Sort: q.Get("sort")}
	byRole := map[string]int{}
	for _, g := range sum.Games {
		byRole[g.Role]++
	}
	for _, p := range meta.Positions {
		d.Positions = append(d.Positions, positionOpt{Key: p, Label: meta.PositionName(p), Games: byRole[meta.Role(p)]})
	}
	d.Position = q.Get("position")
	if meta.Role(d.Position) == "" {
		d.Position = meta.Position(mainRole(sum.Games))
		if d.Position == "" {
			d.Position = "mid"
		}
	}
	less, ok := tierSorts[d.Sort]
	if !ok {
		d.Sort, less = "rank", tierSorts["rank"]
	}

	d.List, d.Err = s.tierList(r.Context())
	if d.Err != nil && !errors.Is(d.Err, errMetaOff) {
		s.log.Printf("tier list: %v", d.Err)
	}
	role := meta.Role(d.Position)
	mine := map[string]*analysis.Stats{}
	for _, g := range sum.Games {
		if g.Role != role {
			continue
		}
		k := normKey(g.Champion)
		if mine[k] == nil {
			mine[k] = &analysis.Stats{}
		}
		mine[k].Add(g)
	}
	for _, e := range d.List.Position(d.Position) {
		row := tierRow{TierEntry: e, MineAdj: math.NaN()}
		if st := mine[normKey(e.Key)]; st != nil {
			row.Mine = *st
			row.MineAdj = st.AdjustedWR(sum.Base)
			// Link to my champion page with the archive's spelling.
			for _, g := range sum.Games {
				if normKey(g.Champion) == normKey(e.Key) {
					row.Key = g.Champion
					break
				}
			}
		}
		d.Rows = append(d.Rows, row)
	}
	sort.SliceStable(d.Rows, func(i, j int) bool { return less(d.Rows[i], d.Rows[j]) })
	s.render(w, r, "tierlist", "", d)
}

// poolFit is the champions page's comparison with the meta.
func (s *Server) poolFit(r *http.Request, sum analysis.Summary) ([]meta.Fit, meta.TierList, error) {
	tl, err := s.tierList(r.Context())
	if err != nil {
		if !errors.Is(err, errMetaOff) {
			s.log.Printf("tier list: %v", err)
		}
		return nil, tl, err
	}
	return meta.PoolFit(sum.Games, sum.Base, tl, 1), tl, nil
}

type championMeta struct {
	meta.Analysis
	Err      error
	Position string
	// Good and Bad are OP.GG's best and worst matchups, with my record
	// against each.
	Good, Bad []counterRow
}

type counterRow struct {
	meta.Counter
	Mine  analysis.Stats
	Patch string // for icons
}

// championMeta fetches OP.GG's analysis of a champion in the role I play
// it most, joined with my record into its listed counters.
func (s *Server) championMeta(r *http.Request, name, patch string, games []analysis.Game) championMeta {
	if s.meta == nil {
		return championMeta{Err: errMetaOff}
	}
	pos := meta.Position(mainRole(games))
	if pos == "" {
		return championMeta{Err: errors.New("no known role for this champion")}
	}
	ctx, cancel := context.WithTimeout(r.Context(), metaTimeout)
	defer cancel()
	a, err := s.meta.Analysis(ctx, name, pos)
	if err != nil {
		s.log.Printf("analysis %s %s: %v", name, pos, err)
		return championMeta{Err: err, Position: pos}
	}
	cm := championMeta{Analysis: a, Position: pos}
	vs := map[string]*analysis.Stats{}
	for _, g := range games {
		if g.Opponent == "" || g.Role != meta.Role(pos) {
			continue
		}
		k := normKey(g.Opponent)
		if vs[k] == nil {
			vs[k] = &analysis.Stats{}
		}
		vs[k].Add(g)
	}
	row := func(c meta.Counter) counterRow {
		cr := counterRow{Counter: c, Patch: patch}
		if st := vs[normKey(c.Key)]; st != nil {
			cr.Mine = *st
		}
		return cr
	}
	for _, c := range a.Good {
		cm.Good = append(cm.Good, row(c))
	}
	for _, c := range a.Bad {
		cm.Bad = append(cm.Bad, row(c))
	}
	return cm
}

// normKey folds champion keys so match-v5's FiddleSticks meets OP.GG's
// Fiddlesticks.
func normKey(k string) string {
	b := []byte(k)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}
