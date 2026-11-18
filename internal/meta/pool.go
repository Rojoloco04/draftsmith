package meta

import (
	"fmt"
	"sort"

	"github.com/rojoloco04/draftsmith/internal/analysis"
)

// PoolMinGames is the fewest games on a champion before it is compared
// with the meta.
const PoolMinGames = 3

// Fit compares my results on a champion with its place in the meta, in
// the role I play it most.
type Fit struct {
	Champion string // Riot key
	Role     string // Riot position
	Position string // OP.GG position
	analysis.Stats
	Adjusted float64
	// Mine is "good", "bad" or "even": adjusted win rate at least 3 points
	// above, below, or within 3 points of my overall win rate (the same
	// threshold the report uses for strengths and drains).
	Mine string
	// Meta is the tier list entry; InMeta is false if OP.GG doesn't list
	// the champion in that position.
	Meta   TierEntry
	InMeta bool
	Note   string
}

// MetaRead is "strong" (tier 1-2), "weak" (tier 4-5), "average" (tier 3)
// or "unlisted".
func (f Fit) MetaRead() string {
	switch {
	case !f.InMeta:
		return "unlisted"
	case f.Meta.Tier <= 2:
		return "strong"
	case f.Meta.Tier >= 4:
		return "weak"
	}
	return "average"
}

// PoolFit lines up every champion I've played at least PoolMinGames times
// (remakes excluded) with the tier list, most played first.
func PoolFit(games []analysis.Game, base float64, tl TierList) []Fit {
	byChamp := map[string][]analysis.Game{}
	for _, g := range games {
		if !g.Remake {
			byChamp[g.Champion] = append(byChamp[g.Champion], g)
		}
	}
	var out []Fit
	for champ, gs := range byChamp {
		if len(gs) < PoolMinGames {
			continue
		}
		roles := analysis.GroupBy(gs, func(g analysis.Game) string { return g.Role })
		sort.SliceStable(roles, func(i, j int) bool {
			if roles[i].Games != roles[j].Games {
				return roles[i].Games > roles[j].Games
			}
			return roles[i].Key < roles[j].Key
		})
		f := Fit{Champion: champ, Role: roles[0].Key, Position: Position(roles[0].Key)}
		for _, g := range gs {
			f.Stats.Add(g)
		}
		f.Adjusted = f.AdjustedWR(base)
		switch {
		case f.Adjusted >= base+0.03:
			f.Mine = "good"
		case f.Adjusted <= base-0.03:
			f.Mine = "bad"
		default:
			f.Mine = "even"
		}
		f.Meta, f.InMeta = tl.Find(champ, f.Position)
		f.Note = fitNote(f)
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Games != out[j].Games {
			return out[i].Games > out[j].Games
		}
		return out[i].Champion < out[j].Champion
	})
	return out
}

func fitNote(f Fit) string {
	pos := PositionName(f.Position)
	tier := fmt.Sprintf("tier %d %s", f.Meta.Tier, pos)
	switch f.MetaRead() {
	case "unlisted":
		switch f.Mine {
		case "good":
			return fmt.Sprintf("Working for you, though OP.GG doesn't list it %s. A pocket pick.", pos)
		case "bad":
			return fmt.Sprintf("Not working for you, and OP.GG doesn't list it %s. Hard to justify in ranked.", pos)
		}
		return fmt.Sprintf("Even for you; OP.GG doesn't list it %s.", pos)
	case "strong":
		switch f.Mine {
		case "good":
			return fmt.Sprintf("Strong for you and strong in the meta (%s). Keep playing it.", tier)
		case "bad":
			return fmt.Sprintf("Strong in the meta (%s) but losing for you. Worth working out why before dropping it.", tier)
		}
		return fmt.Sprintf("Even for you, strong in the meta (%s). Room to grow into it.", tier)
	case "weak":
		switch f.Mine {
		case "good":
			return fmt.Sprintf("You win on it, but it's weak this patch (%s). Your results, not the meta, are the reason to keep it.", tier)
		case "bad":
			return fmt.Sprintf("Losing for you and weak this patch (%s). The easiest one to drop.", tier)
		}
		return fmt.Sprintf("Even for you, weak this patch (%s).", tier)
	}
	switch f.Mine {
	case "good":
		return fmt.Sprintf("Working for you; middle of the meta (%s).", tier)
	case "bad":
		return fmt.Sprintf("Losing for you; middle of the meta (%s).", tier)
	}
	return fmt.Sprintf("Even for you; middle of the meta (%s).", tier)
}
