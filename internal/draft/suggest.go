package draft

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/rojoloco04/draftsmith/internal/analysis"
	"github.com/rojoloco04/draftsmith/internal/meta"
)

// MatchupWeight is how many games of the champion's usual win rate a
// matchup's record is blended with. OP.GG matchups run from a few hundred
// to several thousand games; this keeps a thin one from swinging the score.
const MatchupWeight = 500.0

// MinOpponentOdds is the least chance an enemy is my laner for its matchup
// to count or be mentioned.
const MinOpponentOdds = 0.02

// Input is everything Suggest needs; nothing here calls the network.
type Input struct {
	// Position is my lane, in OP.GG's spelling (meta.Positions).
	Position string
	// Games are my games (remakes are skipped); Base is my overall win
	// rate, the same one the dashboard adjusts toward.
	Games []analysis.Game
	Base  float64
	// MinGames is the fewest games on a champion in Position for it to be
	// suggested. Champions I haven't played there aren't suggested.
	MinGames int

	TierList meta.TierList
	// Odds are the enemy team's lane odds (Roles).
	Odds RoleOdds
	// Matchups are OP.GG's matchup tables per candidate (Riot key) in
	// Position, each row counting the candidate's wins. A candidate without
	// one is scored without matchups.
	Matchups map[string][]meta.Counter

	// Taken are champions no one can pick any more: bans and locked picks
	// on both teams. Hovered are champions a teammate is hovering. Both
	// hold Riot keys.
	Taken, Hovered []string
}

// Opponent is one possible lane opponent for a suggestion.
type Opponent struct {
	Champion string  // Riot key
	Odds     float64 // chance they're my laner
	WinRate  float64 // my champion into them, smoothed (NaN without data)
	Games    int     // OP.GG games in the matchup (0 without data)
}

// Suggestion is one champion to consider, with the numbers behind it.
type Suggestion struct {
	Champion string // Riot key
	// My record on it in Position.
	Games, Wins int
	// MetaWinRate is OP.GG's win rate for it in Position (NaN if unlisted);
	// Meta is its tier list entry.
	MetaWinRate float64
	Meta        meta.TierEntry
	InMeta      bool
	// Personal is my expected win rate on it before the draft: my record,
	// blended with analysis.PriorWeight games at my base shifted by its
	// meta strength.
	Personal float64
	// Matchup is how much the likely lane opponents move that, in log
	// odds (0 when no opponent is known).
	Matchup   float64
	Opponents []Opponent
	// WinProb is Personal shifted by Matchup: the score suggestions are
	// ranked by.
	WinProb float64
	// TeammateHovering is set if a teammate is hovering it.
	TeammateHovering bool
}

// Suggest scores every champion I've played at least MinGames times in my
// lane and isn't taken, best first.
func Suggest(in Input) []Suggestion {
	role := meta.Role(in.Position)
	taken := keySet(in.Taken)
	hovered := keySet(in.Hovered)
	minGames := max(in.MinGames, 1)

	byChamp := map[string]*analysis.Stats{}
	var order []string
	for _, g := range in.Games {
		if g.Remake || g.Role != role {
			continue
		}
		st, ok := byChamp[g.Champion]
		if !ok {
			st = &analysis.Stats{}
			byChamp[g.Champion] = st
			order = append(order, g.Champion)
		}
		st.Add(g)
	}

	var out []Suggestion
	for _, champ := range order {
		st := byChamp[champ]
		if st.Games < minGames || taken[norm(champ)] {
			continue
		}
		s := Suggestion{Champion: champ, Games: st.Games, Wins: st.Wins, MetaWinRate: math.NaN(), TeammateHovering: hovered[norm(champ)]}
		s.Meta, s.InMeta = in.TierList.Find(champ, in.Position)
		metaWR := 0.5
		if s.InMeta {
			s.MetaWinRate = s.Meta.WinRate()
			metaWR = s.MetaWinRate
		}
		prior := clamp(in.Base + metaWR - 0.5)
		s.Personal = st.AdjustedWR(prior)

		lane := in.Odds.Lane(in.Position)
		table := in.Matchups[champ]
		for i, enemy := range in.Odds.Champions {
			if enemy == "" || i >= len(lane) || lane[i] < MinOpponentOdds {
				continue
			}
			o := Opponent{Champion: enemy, Odds: lane[i], WinRate: math.NaN()}
			if row, ok := findCounter(table, enemy); ok {
				o.Games = row.Play
				o.WinRate = (float64(row.Win) + MatchupWeight*metaWR) / (float64(row.Play) + MatchupWeight)
				s.Matchup += o.Odds * (logit(o.WinRate) - logit(metaWR))
			}
			s.Opponents = append(s.Opponents, o)
		}
		sort.SliceStable(s.Opponents, func(i, j int) bool { return s.Opponents[i].Odds > s.Opponents[j].Odds })
		s.WinProb = s.Personal
		if s.Matchup != 0 {
			s.WinProb = sigmoid(logit(s.Personal) + s.Matchup)
		}
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].WinProb != out[j].WinProb {
			return out[i].WinProb > out[j].WinProb
		}
		return out[i].Games > out[j].Games
	})
	return out
}

// Reason is a one-line explanation of a suggestion. name turns a Riot key
// into a display name (nil uses keys).
func (s Suggestion) Reason(position string, name func(string) string) string {
	if name == nil {
		name = func(k string) string { return k }
	}
	var parts []string
	parts = append(parts, fmt.Sprintf("you're %d-%d on it %s", s.Wins, s.Games-s.Wins, meta.PositionName(position)))
	if s.InMeta {
		parts = append(parts, fmt.Sprintf("tier %d, %s in Emerald+", s.Meta.Tier, analysis.Pct(s.MetaWinRate)))
	} else {
		parts = append(parts, "not in OP.GG's "+meta.PositionName(position)+" list")
	}
	var mu []string
	for _, o := range s.Opponents {
		if len(mu) == 2 {
			break
		}
		where := fmt.Sprintf("%s %s", strings.ToLower(meta.PositionName(position)), analysis.Pct(o.Odds))
		if math.IsNaN(o.WinRate) {
			mu = append(mu, fmt.Sprintf("no matchup data vs %s (%s)", name(o.Champion), where))
		} else {
			mu = append(mu, fmt.Sprintf("%s into %s (%s)", analysis.Pct(o.WinRate), name(o.Champion), where))
		}
	}
	if len(mu) == 0 {
		mu = append(mu, "lane opponent not picked yet")
	}
	parts = append(parts, strings.Join(mu, ", "))
	if s.TeammateHovering {
		parts = append(parts, "a teammate is hovering it")
	}
	return fmt.Sprintf("%s %s: %s", name(s.Champion), analysis.Pct(s.WinProb), strings.Join(parts, " · "))
}

// LikelyOpponent is the most likely lane opponent, if any enemy is at
// least minOdds likely to be it.
func (s Suggestion) LikelyOpponent(minOdds float64) (string, bool) {
	if len(s.Opponents) > 0 && s.Opponents[0].Odds >= minOdds {
		return s.Opponents[0].Champion, true
	}
	return "", false
}

func findCounter(table []meta.Counter, key string) (meta.Counter, bool) {
	k := norm(key)
	for _, c := range table {
		if norm(c.Key) == k || norm(c.Name) == k {
			return c, true
		}
	}
	return meta.Counter{}, false
}

func keySet(keys []string) map[string]bool {
	m := map[string]bool{}
	for _, k := range keys {
		m[norm(k)] = true
	}
	return m
}

// norm folds a champion key or name for matching ("Cho'Gath" = "Chogath").
func norm(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func logit(p float64) float64   { p = clamp(p); return math.Log(p / (1 - p)) }
func sigmoid(x float64) float64 { return 1 / (1 + math.Exp(-x)) }
func clamp(p float64) float64   { return math.Min(math.Max(p, 0.01), 0.99) }
