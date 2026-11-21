package draft

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/rojoloco04/draftsmith/internal/analysis"
	"github.com/rojoloco04/draftsmith/internal/lcu"
	"github.com/rojoloco04/draftsmith/internal/meta"
)

// Source is the OP.GG data Live uses; *meta.Service implements it.
type Source interface {
	TierList(ctx context.Context) (meta.TierList, error) // read once, by Start
	MatchupTable(ctx context.Context, key, position string) ([]meta.Counter, error)
	Matchup(ctx context.Context, key, opponent, position string) (meta.Matchup, error)
	Analysis(ctx context.Context, key, position string) (meta.Analysis, error)
}

// Top is how many suggestions Live shows.
const Top = 3

// LikelyOdds is how sure Live must be of my lane opponent to show the rune
// page for that matchup rather than the champion's usual one.
const LikelyOdds = 0.5

// Live turns champ select updates into suggestions. Update scores from
// whatever is cached and returns at once; missing OP.GG data is fetched in
// the background, and the suggestions are logged again when it arrives.
// It only reads and logs: it never acts in the client.
type Live struct {
	Meta     Source
	Games    []analysis.Game // my games, for my record per champion
	Base     float64         // my overall win rate
	MinGames int
	// Key maps a champion id to its Riot key ("" if unknown); Name a Riot
	// key to a display name (nil shows keys).
	Key  func(id int) string
	Name func(key string) string
	Log  *log.Logger
	// OnBoard, if set, is called with the champ select whenever anything
	// on it changes, including a rune page arriving. It's called from the
	// goroutine that called Update or from the background fetcher, one
	// call at a time, and must not call back into Live.
	OnBoard func(Board)

	rmu     sync.Mutex // serializes refresh, so "changed" compares in order
	mu      sync.Mutex
	ctx     context.Context
	tl      meta.TierList
	tlErr   error
	sess    lcu.Session
	sessAt  time.Time // when sess was read, for the timer
	have    bool
	tables  map[string][]meta.Counter // key/position
	runes   map[string]runeResult     // key/opponent/position
	pending map[string]bool
	// queue holds background fetches; urgent ones (my own champion's rune
	// page) go first.
	queue, urgent chan func()
	last          view
	lastB         string // last board's fingerprint
}

// view is what Live shows for one champ select state: a header, and
// for each suggestion (or my locked pick) its reason and rune page.
type view struct {
	head  string
	items []viewItem
}

type viewItem struct {
	name, reason, runes string
}

const loadingRunes = "(loading from OP.GG)"

// ranking is the view without rune pages: when it changes, the whole view
// is logged again; when only rune pages arrive, just those are.
func (v view) ranking() string {
	var b strings.Builder
	b.WriteString(v.head)
	for _, it := range v.items {
		b.WriteString("\n" + it.name + "\t" + it.reason)
	}
	return b.String()
}

func (v view) String() string {
	if len(v.items) == 1 && v.items[0].reason == "" { // locked in
		return v.head + "; runes " + v.items[0].runes
	}
	var b strings.Builder
	b.WriteString(v.head)
	for i, it := range v.items {
		fmt.Fprintf(&b, "\n  %d. %s\n     runes %s", i+1, it.reason, it.runes)
	}
	return b.String()
}

// runeResult is a rune page and the spells and items from the same OP.GG
// response.
type runeResult struct {
	page  meta.RunePage
	build meta.Build
	vs    string // the opponent the page is for, "" for the champion's usual page
	ok    bool
}

// String is the page and spells in one line.
func (r runeResult) String() string {
	s := r.page.String()
	if sp, ok := r.build.TopSpells(); ok {
		s += "; spells " + meta.SpellNames(sp.IDs)
	}
	return s
}

// Start loads the tier list and begins the background fetcher; it stops
// with ctx. The tier list is read once here, so champ select updates never
// wait on OP.GG.
func (l *Live) Start(ctx context.Context) {
	tl, err := l.Meta.TierList(ctx)
	l.mu.Lock()
	l.tl, l.tlErr = tl, err
	l.ctx = ctx
	l.tables = map[string][]meta.Counter{}
	l.runes = map[string]runeResult{}
	l.pending = map[string]bool{}
	l.queue = make(chan func(), 64)
	l.urgent = make(chan func(), 16)
	l.mu.Unlock()
	go func() {
		for {
			select {
			case f := <-l.urgent:
				f()
				continue
			default:
			}
			select {
			case <-ctx.Done():
				return
			case f := <-l.urgent:
				f()
			case f := <-l.queue:
				f()
			}
		}
	}()
}

// Warm fetches matchup tables for my pool in position ("" for the lane I
// play most) ahead of champ select, so the first suggestions don't wait on
// OP.GG.
func (l *Live) Warm(position string) {
	if position == "" {
		position = l.mainPosition()
	}
	for _, c := range l.pool(position) {
		l.fetchTable(c, position)
	}
}

// Reset forgets the last champ select, so the next one logs from scratch.
func (l *Live) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.have, l.last, l.lastB = false, view{}, ""
}

// Update handles a champ select update.
func (l *Live) Update(s lcu.Session) {
	l.mu.Lock()
	l.sess, l.sessAt, l.have = s, time.Now(), true
	l.mu.Unlock()
	l.refresh()
}

// refresh scores the current session and logs the result if it changed.
func (l *Live) refresh() {
	l.rmu.Lock()
	defer l.rmu.Unlock()
	l.mu.Lock()
	s, at, have := l.sess, l.sessAt, l.have
	l.mu.Unlock()
	if !have || l.ctx == nil {
		return
	}
	v, b := l.render(s, at)
	fp := b.fingerprint()
	l.mu.Lock()
	prev, prevB := l.last, l.lastB
	l.last, l.lastB = v, fp
	l.mu.Unlock()
	if l.OnBoard != nil && fp != prevB {
		l.OnBoard(b)
	}
	switch {
	case v.head == "":
	case v.ranking() != prev.ranking():
		l.Log.Print(v)
	default:
		for i, it := range v.items {
			if it.runes != prev.items[i].runes && it.runes != loadingRunes {
				l.Log.Printf("  %s runes %s", it.name, it.runes)
			}
		}
	}
}

// render works out the suggestions for s, read at time at, queuing
// fetches for anything missing. It returns them for the log and, with the
// rest of the draft, for the dashboard.
func (l *Live) render(s lcu.Session, at time.Time) (view, Board) {
	l.mu.Lock()
	tl, err := l.tl, l.tlErr
	l.mu.Unlock()
	var enemies []string
	for _, m := range s.TheirTeam {
		enemies = append(enemies, l.key(m.ChampionID)) // a hover shows in the client too
	}
	odds := Roles(tl, enemies, nil) // without a tier list, every lane is as likely
	b := l.board(s, at, odds)

	me, ok := s.Me()
	if !ok {
		return view{}, b
	}
	position := clientPosition(me.AssignedPosition)
	if position == "" {
		position = l.mainPosition()
	}
	b.Position = position
	if position == "" {
		b.Note = "No lane assigned, and no games to guess one from."
		return view{head: "suggestions: no lane assigned and no games to guess one from"}, b
	}
	if err != nil {
		b.Note = "No OP.GG tier list: " + err.Error()
		return view{head: "suggestions: no OP.GG tier list: " + err.Error()}, b
	}

	var taken, hovered, allies []string
	for _, m := range s.MyTeam {
		if m.CellID == me.CellID {
			continue
		}
		if id := pickOrHover(m); id != 0 {
			allies = append(allies, l.key(id))
		}
	}
	b.Comp = Comp(allies)
	mine, theirs := s.BanList()
	for _, id := range append(mine, theirs...) {
		taken = append(taken, l.key(id))
	}
	for _, m := range append(append([]lcu.Member(nil), s.MyTeam...), s.TheirTeam...) {
		switch {
		case m.ChampionID != 0 && s.Locked(m.CellID):
			taken = append(taken, l.key(m.ChampionID))
		case m.CellID == me.CellID || m.Team != me.Team:
		case m.ChampionID != 0:
			hovered = append(hovered, l.key(m.ChampionID))
		case m.ChampionPickIntent != 0:
			hovered = append(hovered, l.key(m.ChampionPickIntent))
		}
	}

	// Once I've locked in, only my champion's runes matter.
	if me.ChampionID != 0 && s.Locked(me.CellID) {
		champ := l.key(me.ChampionID)
		sug := Suggest(Input{Position: position, Games: l.Games, Base: l.Base, TierList: tl, Odds: odds,
			Matchups: l.tablesFor([]string{champ}, position), Allies: allies})
		var forMe *Suggestion
		for i := range sug {
			if norm(sug[i].Champion) == norm(champ) {
				forMe = &sug[i]
			}
		}
		opp := ""
		if forMe != nil {
			opp, _ = forMe.LikelyOpponent(LikelyOdds)
		} else {
			opp = likelyLaner(odds, position)
		}
		b.Locked = true
		b.Picks = []Pick{l.pick(champ, forMe, position, opp, true)}
		return view{
			head:  fmt.Sprintf("locked in %s (%s)", l.name(champ), meta.PositionName(position)),
			items: []viewItem{{name: l.name(champ), runes: l.runeLine(champ, opp, position) + l.buildLine(champ, opp, position)}},
		}, b
	}

	var pool []string
	gone := keySet(taken)
	for _, c := range l.pool(position) {
		if !gone[norm(c)] {
			pool = append(pool, c)
		}
	}
	// My hover (during my turn, or declared before it) gets a card of
	// its own even when it isn't a top suggestion, so its runes show.
	myHover := ""
	if id := pickOrHover(me); id != 0 {
		myHover = l.key(id)
		if !gone[norm(myHover)] && !containsKey(pool, myHover) {
			l.tablesFor([]string{myHover}, position)
		}
	}
	sug := Suggest(Input{
		Position: position, Games: l.Games, Base: l.Base, MinGames: l.MinGames, TierList: tl, Odds: odds,
		Matchups: l.tablesFor(pool, position), Taken: taken, Hovered: hovered, Allies: allies,
	})
	if myHover != "" {
		b.Picks = append(b.Picks, l.hoverPick(myHover, sug, position, tl, odds, allies))
	}
	if len(sug) == 0 {
		b.Note = fmt.Sprintf("None of your champions with %d+ games %s is still available.", max(l.MinGames, 1), meta.PositionName(position))
		return view{head: fmt.Sprintf("suggestions (%s): none; no champion with %d+ games there is still available", meta.PositionName(position), max(l.MinGames, 1))}, b
	}
	v := view{head: fmt.Sprintf("suggestions (%s):", meta.PositionName(position))}
	for i, sg := range sug {
		if i == Top {
			break
		}
		opp, _ := sg.LikelyOpponent(LikelyOdds)
		v.items = append(v.items, viewItem{name: l.name(sg.Champion), reason: sg.Reason(position, l.Name), runes: l.runeLine(sg.Champion, opp, position)})
		if myHover != "" && norm(sg.Champion) == norm(myHover) {
			continue // already shown as my hover
		}
		b.Picks = append(b.Picks, l.pick(sg.Champion, &sug[i], position, opp, false))
	}
	return v, b
}

// runeLine describes the rune page for champ into opp ("" if unknown),
// queuing the fetch if it isn't here yet.
func (l *Live) runeLine(champ, opp, position string) string {
	r, ok := l.runeFor(champ, opp, position, false)
	if !ok {
		return loadingRunes
	}
	if !r.ok {
		return "(none on OP.GG)"
	}
	if r.vs != "" {
		return "vs " + l.name(r.vs) + ": " + r.String()
	}
	return "(usual page): " + r.String()
}

// buildLine is "; build ..." for champ into opp once it's fetched, else "".
func (l *Live) buildLine(champ, opp, position string) string {
	l.mu.Lock()
	r, ok := l.runes[champ+"/"+opp+"/"+position]
	l.mu.Unlock()
	if !ok || !r.ok || r.build.Empty() {
		return ""
	}
	return "; build " + r.build.String()
}

// runeFor returns the rune page for champ into opp, and whether it's
// been fetched; if not, it queues the fetch.
func (l *Live) runeFor(champ, opp, position string, urgent bool) (runeResult, bool) {
	l.mu.Lock()
	r, ok := l.runes[champ+"/"+opp+"/"+position]
	l.mu.Unlock()
	if !ok {
		l.fetchRunes(champ, opp, position, urgent)
	}
	return r, ok
}

// usualRunes is champ's usual page if it's already fetched, to show while
// a matchup page loads.
func (l *Live) usualRunes(champ, position string) (runeResult, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	r, ok := l.runes[champ+"//"+position]
	return r, ok && r.ok
}

func (l *Live) tablesFor(champs []string, position string) map[string][]meta.Counter {
	out := map[string][]meta.Counter{}
	for _, c := range champs {
		l.mu.Lock()
		t, ok := l.tables[c+"/"+position]
		l.mu.Unlock()
		if ok {
			out[c] = t
		} else {
			l.fetchTable(c, position)
		}
	}
	return out
}

func containsKey(keys []string, k string) bool {
	for _, x := range keys {
		if norm(x) == norm(k) {
			return true
		}
	}
	return false
}

// pickOrHover is a member's champion: the pick or hover, else the pick
// declared before their turn (0 for none).
func pickOrHover(m lcu.Member) int {
	if m.ChampionID != 0 {
		return m.ChampionID
	}
	return m.ChampionPickIntent
}

func (l *Live) fetchTable(champ, position string) {
	k := "table/" + champ + "/" + position
	l.enqueue(k, false, func() {
		t, err := l.Meta.MatchupTable(l.ctx, champ, position)
		if err != nil && !errors.Is(err, meta.ErrNoData) && l.ctx.Err() == nil {
			l.Log.Printf("OP.GG matchups for %s %s: %v", l.name(champ), meta.PositionName(position), err)
		}
		l.mu.Lock()
		l.tables[champ+"/"+position] = t // nil on failure: scored without matchups, not retried
		l.mu.Unlock()
	})
}

func (l *Live) fetchRunes(champ, opp, position string, urgent bool) {
	k := "runes/" + champ + "/" + opp + "/" + position
	l.enqueue(k, urgent, func() {
		r := runeResult{}
		if opp != "" {
			if m, err := l.Meta.Matchup(l.ctx, champ, opp, position); err == nil && len(m.Runes) > 0 {
				r = runeResult{page: m.Runes[0], build: m.Build, vs: opp, ok: true}
			}
		}
		if !r.ok {
			if a, err := l.Meta.Analysis(l.ctx, champ, position); err == nil && len(a.Runes.Primary) > 0 {
				r = runeResult{page: a.Runes, build: a.Build, ok: true}
			} else if err != nil && !errors.Is(err, meta.ErrNoData) && l.ctx.Err() == nil {
				l.Log.Printf("OP.GG runes for %s %s: %v", l.name(champ), meta.PositionName(position), err)
			}
		}
		l.mu.Lock()
		l.runes[champ+"/"+opp+"/"+position] = r
		l.mu.Unlock()
	})
}

// enqueue runs f in the background once per key, then refreshes the
// suggestions with what it fetched. Urgent jobs run before the rest.
func (l *Live) enqueue(key string, urgent bool, f func()) {
	l.mu.Lock()
	if l.pending == nil || l.pending[key] {
		l.mu.Unlock()
		return
	}
	l.pending[key] = true
	q := l.queue
	if urgent {
		q = l.urgent
	}
	l.mu.Unlock()
	job := func() {
		f()
		l.refresh()
	}
	select {
	case q <- job:
	default: // queue full: drop it and allow a later retry
		l.mu.Lock()
		delete(l.pending, key)
		l.mu.Unlock()
	}
}

// pool is every champion I've played MinGames+ times in position.
func (l *Live) pool(position string) []string {
	role := meta.Role(position)
	n := map[string]int{}
	var order []string
	for _, g := range l.Games {
		if g.Remake || g.Role != role {
			continue
		}
		if n[g.Champion] == 0 {
			order = append(order, g.Champion)
		}
		n[g.Champion]++
	}
	var out []string
	for _, c := range order {
		if n[c] >= max(l.MinGames, 1) {
			out = append(out, c)
		}
	}
	return out
}

// mainPosition is the lane I've played most, for blind pick where the
// client assigns none.
func (l *Live) mainPosition() string {
	n := map[string]int{}
	best, bn := "", 0
	for _, g := range l.Games {
		if g.Remake {
			continue
		}
		n[g.Role]++
		if n[g.Role] > bn {
			best, bn = g.Role, n[g.Role]
		}
	}
	return meta.Position(best)
}

func likelyLaner(o RoleOdds, position string) string {
	best, bp := "", LikelyOdds
	for i, p := range o.Lane(position) {
		if o.Champions[i] != "" && p >= bp {
			best, bp = o.Champions[i], p
		}
	}
	return best
}

func (l *Live) key(id int) string {
	if id == 0 || l.Key == nil {
		return ""
	}
	return l.Key(id)
}

func (l *Live) name(key string) string {
	if l.Name != nil {
		if n := l.Name(key); n != "" {
			return n
		}
	}
	return key
}

// clientPosition maps the client's assignedPosition to OP.GG's position.
func clientPosition(p string) string {
	switch p {
	case "top", "jungle":
		return p
	case "middle":
		return "mid"
	case "bottom":
		return "adc"
	case "utility":
		return "support"
	}
	return ""
}
