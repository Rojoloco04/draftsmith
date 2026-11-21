package draft

import (
	"fmt"
	"sort"
	"time"

	"github.com/rojoloco04/draftsmith/internal/lcu"
	"github.com/rojoloco04/draftsmith/internal/meta"
)

// Board is one champ select as Live sees it, for the dashboard: both
// teams, the bans, and the suggestions with their rune pages. Like the
// session it comes from, it holds champions and lanes only, never who is
// playing them.
type Board struct {
	// Phase is the champ select timer's phase (PLANNING, BAN_PICK,
	// FINALIZATION, GAME_STARTING) and Ends when its countdown runs out
	// (zero when the timer is off or infinite).
	Phase string
	Ends  time.Time
	// Position is my lane in OP.GG's spelling, from the client or, in
	// blind pick, the lane I play most.
	Position string
	// Patch is the newest patch among my games, for icon URLs.
	Patch string

	// MyTeam is in lane order; TheirTeam in the client's cell order.
	MyTeam, TheirTeam []Slot
	MyBans, TheirBans []Champ

	// Comp is my teammates' champions so far, for damage mix and
	// frontline.
	Comp TeamComp

	// Note says why there are no suggestions, when there are none.
	Note string
	// Picks are the top suggestions, best first. Once I've locked in it
	// holds only my champion, with Locked set.
	Picks  []Pick
	Locked bool
}

// Champ is a champion by Riot key and display name.
type Champ struct {
	Key, Name string
}

// Slot is one player's place in the draft.
type Slot struct {
	Champ // the pick or hover; zero when there's neither
	// Position is the lane: assigned for my team, the likeliest one for
	// the enemy's (with Odds its chance).
	Position string
	Odds     float64
	// Locked is a locked-in pick; otherwise a champion is a hover.
	Locked, Me, Autofill bool
}

// Pick is a suggestion (or my locked-in champion) for the dashboard.
type Pick struct {
	Champ
	Suggestion // the numbers; zero for a locked champion outside my pool
	Scored     bool
	Reason     string
	// Mine is the champion I'm hovering, shown whether or not it's a top
	// suggestion.
	Mine bool
	// Vs are the likeliest lane opponents (at most two), by name.
	Vs []Versus
	// Runes is OP.GG's page: for RunesVs when that's set, otherwise the
	// champion's usual page. RunesState is "loading", "none" or "ok".
	Runes      meta.RunePage
	RunesVs    Champ
	RunesState string
	// Spells is OP.GG's most played summoner spell pair, and Build its
	// items and skill order, from the same source as Runes (zero if it
	// lists none).
	Spells meta.Choice
	Build  meta.Build
}

// Versus is one possible lane opponent for a pick.
type Versus struct {
	Champ
	Odds    float64 // chance they're my laner
	WinRate float64 // my champion into them (NaN without data)
	Games   int     // OP.GG games in the matchup
}

// Rune states.
const (
	RunesLoading = "loading"
	RunesNone    = "none"
	RunesOK      = "ok"
)

// board fills in everything but the picks: phase, timer, teams and bans.
func (l *Live) board(s lcu.Session, at time.Time, odds RoleOdds) Board {
	b := Board{Phase: s.Timer.Phase, Patch: l.patch()}
	if left := s.Timer.AdjustedTimeLeftInPhase; left > 0 && !s.Timer.IsInfinite {
		b.Ends = at.Add(time.Duration(left) * time.Millisecond)
	}
	slot := func(m lcu.Member) Slot {
		sl := Slot{Position: clientPosition(m.AssignedPosition), Me: m.CellID == s.LocalPlayerCellID, Autofill: m.IsAutofilled}
		switch {
		case m.ChampionID != 0:
			sl.Champ, sl.Locked = l.champ(m.ChampionID), s.Locked(m.CellID)
		case m.ChampionPickIntent != 0:
			sl.Champ = l.champ(m.ChampionPickIntent)
		}
		return sl
	}
	mine := append([]lcu.Member(nil), s.MyTeam...)
	sort.SliceStable(mine, func(i, j int) bool {
		if a, b := laneOrder[mine[i].AssignedPosition], laneOrder[mine[j].AssignedPosition]; a != b {
			return a < b
		}
		return mine[i].CellID < mine[j].CellID
	})
	for _, m := range mine {
		b.MyTeam = append(b.MyTeam, slot(m))
	}
	for i, m := range s.TheirTeam {
		sl := slot(m)
		if i < len(odds.P) {
			sl.Position, sl.Odds = odds.Likely(i)
		}
		b.TheirTeam = append(b.TheirTeam, sl)
	}
	myBans, theirBans := s.BanList()
	for _, id := range myBans {
		b.MyBans = append(b.MyBans, l.champ(id))
	}
	for _, id := range theirBans {
		b.TheirBans = append(b.TheirBans, l.champ(id))
	}
	return b
}

// hoverPick is the card for the champion I'm hovering: scored like a
// suggestion if I've played it in this lane at all.
func (l *Live) hoverPick(champ string, sug []Suggestion, position string, tl meta.TierList, odds RoleOdds, allies []string) Pick {
	find := func(ss []Suggestion) *Suggestion {
		for i := range ss {
			if norm(ss[i].Champion) == norm(champ) {
				return &ss[i]
			}
		}
		return nil
	}
	sg := find(sug)
	if sg == nil {
		sg = find(Suggest(Input{Position: position, Games: l.Games, Base: l.Base, TierList: tl, Odds: odds,
			Matchups: l.tablesFor([]string{champ}, position), Allies: allies}))
	}
	opp := likelyLaner(odds, position)
	if sg != nil {
		opp, _ = sg.LikelyOpponent(LikelyOdds)
	}
	p := l.pick(champ, sg, position, opp, true)
	p.Mine = true
	return p
}

// pick builds a suggestion's card. sg is nil for a locked champion
// outside my pool. urgent puts its rune fetch ahead of the rest: it's set
// for my own champion.
func (l *Live) pick(champ string, sg *Suggestion, position, opp string, urgent bool) Pick {
	p := Pick{Champ: Champ{Key: champ, Name: l.name(champ)}}
	if sg != nil {
		p.Suggestion, p.Scored = *sg, true
		p.Reason = sg.Reason(position, l.Name)
		for _, o := range sg.Opponents {
			if len(p.Vs) == 2 {
				break
			}
			p.Vs = append(p.Vs, Versus{Champ: Champ{Key: o.Champion, Name: l.name(o.Champion)}, Odds: o.Odds, WinRate: o.WinRate, Games: o.Games})
		}
	}
	r, loaded := l.runeFor(champ, opp, position, urgent)
	if !loaded && opp != "" {
		// Show the usual page while the matchup's loads.
		r, loaded = l.usualRunes(champ, position)
	}
	switch {
	case !loaded:
		p.RunesState = RunesLoading
	case !r.ok:
		p.RunesState = RunesNone
	default:
		p.RunesState, p.Runes, p.Build = RunesOK, r.page, r.build
		p.Spells, _ = r.build.TopSpells()
		if r.vs != "" {
			p.RunesVs = Champ{Key: r.vs, Name: l.name(r.vs)}
		}
	}
	return p
}

// fingerprint is a stable text form of b, to tell whether it changed.
// (Boards hold NaNs, which reflect.DeepEqual never finds equal.)
func (b Board) fingerprint() string { return fmt.Sprintf("%+v", b) }

func (l *Live) champ(id int) Champ {
	k := l.key(id)
	if k == "" {
		return Champ{Name: fmt.Sprintf("#%d", id)}
	}
	return Champ{Key: k, Name: l.name(k)}
}

// patch is the newest patch I've played on.
func (l *Live) patch() string {
	var at time.Time
	patch := ""
	for _, g := range l.Games {
		if g.Start.After(at) {
			at, patch = g.Start, g.Patch
		}
	}
	return patch
}

// laneOrder sorts the client's assignedPosition values in lane order,
// with "" (unknown) last.
var laneOrder = map[string]int{"top": 1, "jungle": 2, "middle": 3, "bottom": 4, "utility": 5, "": 6}
