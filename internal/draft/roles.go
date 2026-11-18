// Package draft turns a champ select into odds and suggestions: which
// enemy plays which lane, and which of my champions to pick into them.
//
// Everything here is plain arithmetic over data Draftsmith already has
// (OP.GG's tier list and my own games), so it runs offline, in
// microseconds, and gives the same answer every time.
package draft

import (
	"math"

	"github.com/rojoloco04/draftsmith/internal/meta"
)

// Positions are the five lanes, in meta.Positions order and spelling.
var Positions = meta.Positions

// offRole is the least play share assumed for a champion in a lane where
// OP.GG doesn't list it. It keeps off-role picks possible without letting
// them compete with real ones.
const offRole = 0.005

// RoleOdds is, for each enemy slot, the probability of each lane.
type RoleOdds struct {
	// Champions are the enemy slots as given: a Riot key, or "" for a
	// player who hasn't picked yet.
	Champions []string
	// P[i][j] is the probability that slot i plays Positions[j].
	P [][]float64
}

// Lane returns, for one lane, each slot's probability of playing it.
func (o RoleOdds) Lane(position string) []float64 {
	j := laneIndex(position)
	out := make([]float64, len(o.P))
	if j < 0 {
		return out
	}
	for i := range o.P {
		out[i] = o.P[i][j]
	}
	return out
}

// Likely returns slot i's most likely lane and its probability.
func (o RoleOdds) Likely(i int) (string, float64) {
	best, bp := "", -1.0
	for j, p := range o.P[i] {
		if p > bp {
			best, bp = Positions[j], p
		}
	}
	return best, bp
}

func laneIndex(position string) int {
	for j, p := range Positions {
		if p == position {
			return j
		}
	}
	return -1
}

// laneShare is how often a champion is played in each lane.
//
// It uses OP.GG's role_rate, the share of the champion's games in each
// lane. The tier list leaves out lanes below a play cutoff, so whatever
// share the listed lanes don't cover is spread evenly over the unlisted
// ones, with at least offRole each. Tier lists cached before role_rate was
// requested fall back to play counts across the listed lanes. A champion
// missing from the tier list entirely gets an even split, and an open
// slot ("") fits every lane equally.
func laneShare(tl meta.TierList, key string) []float64 {
	share := make([]float64, len(Positions))
	if key == "" {
		for j := range share {
			share[j] = 1
		}
		return share
	}
	var listed []int
	var rates, plays float64
	for j, pos := range Positions {
		if e, ok := tl.Find(key, pos); ok {
			listed = append(listed, j)
			share[j] = e.RoleRate
			rates += e.RoleRate
			plays += float64(e.Play)
		}
	}
	switch {
	case len(listed) == 0:
		for j := range share {
			share[j] = 1.0 / float64(len(share))
		}
		return share
	case rates == 0: // no role_rate in this tier list
		for _, j := range listed {
			e, _ := tl.Find(key, Positions[j])
			share[j] = float64(e.Play) / plays
		}
	}
	rest := 0.0
	if unlisted := len(Positions) - len(listed); unlisted > 0 && rates > 0 {
		rest = math.Max(0, 1-rates) / float64(unlisted)
	}
	for j := range share {
		if share[j] == 0 {
			share[j] = math.Max(rest, offRole)
		}
	}
	return share
}

// Roles works out who plays which lane on a team of up to five, from the
// champions picked so far ("" for a slot that hasn't picked).
//
// Every way of giving the five slots the five lanes is weighed by how
// often each picked champion is played in its lane; open slots fit any
// lane equally. Weighing by P(lane | champion) or by P(champion | lane)
// ranks assignments the same, since each lane appears once in every
// assignment. Each slot's odds are summed over all 120 assignments, so a
// flex pick (Seraphine: support or bot) is settled by the rest of the team
// (if Xayah is there, Seraphine is the support).
//
// lanes, if given, fixes known lanes: lanes[i] is slot i's position or "".
// In ranked the client never shows enemy lanes, so it's for my own team.
func Roles(tl meta.TierList, champions []string, lanes []string) RoleOdds {
	n := min(len(champions), len(Positions))
	champions = champions[:n]
	shares := make([][]float64, n)
	for i, c := range champions {
		shares[i] = laneShare(tl, c)
		if i < len(lanes) && laneIndex(lanes[i]) >= 0 {
			fixed := laneIndex(lanes[i])
			for j := range shares[i] {
				if j != fixed {
					shares[i][j] = 0
				}
			}
			shares[i][fixed] = 1
		}
	}

	odds := RoleOdds{Champions: champions, P: make([][]float64, n)}
	for i := range odds.P {
		odds.P[i] = make([]float64, len(Positions))
	}
	total := 0.0
	// Slots take lanes in order; used marks lanes already taken.
	assign := make([]int, n)
	var walk func(i int, used int, w float64)
	walk = func(i int, used int, w float64) {
		if i == n {
			total += w
			for s, j := range assign {
				odds.P[s][j] += w
			}
			return
		}
		for j := range Positions {
			if used&(1<<j) != 0 || shares[i][j] == 0 {
				continue
			}
			assign[i] = j
			walk(i+1, used|1<<j, w*shares[i][j])
		}
	}
	walk(0, 0, 1)
	if total == 0 {
		return odds // contradictory fixed lanes; leave all zero
	}
	for i := range odds.P {
		for j := range odds.P[i] {
			odds.P[i][j] /= total
		}
	}
	return odds
}
