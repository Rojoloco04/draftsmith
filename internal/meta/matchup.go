package meta

import (
	"context"
	"fmt"
	"strings"

	"github.com/rojoloco04/draftsmith/internal/opgg"
)

// MatchupMaxAge is how long a matchup guide counts as fresh.
const MatchupMaxAge = AnalysisMaxAge

// Matchup is OP.GG's lane matchup guide for my champion into one opponent
// (Emerald+, current patch; the tool has no tier filter).
type Matchup struct {
	Freshness
	Key, Opponent string // Riot keys, as asked for
	Position      string
	// Table is the champion's whole matchup table in Position, the same
	// whatever the opponent: Win counts my champion's wins (checked
	// against the opponent's own table, where the two add up to Play).
	Table []Counter
	// Runes are the most played pages in this matchup, most played first.
	// Empty in a mirror request.
	Runes []RunePage
	// Tip, LaneAdvantage ("EVEN" or a champion name) and PlayStyle
	// ("defensive", ...) are OP.GG's notes on the matchup.
	Tip, LaneAdvantage, PlayStyle string
}

// MatchupTable returns key's matchup table in position. It asks for the
// mirror matchup, the smallest response that carries the table.
func (s *Service) MatchupTable(ctx context.Context, key, position string) ([]Counter, error) {
	m, err := s.Matchup(ctx, key, key, position)
	return m.Table, err
}

// Matchup returns the lane matchup guide for key into opponent (Riot keys)
// in position.
func (s *Service) Matchup(ctx context.Context, key, opponent, position string) (Matchup, error) {
	if Role(position) == "" {
		return Matchup{}, fmt.Errorf("unknown position %q", position)
	}
	type counter struct {
		Name string `json:"champion_name"`
		Play int    `json:"play"`
		Win  int    `json:"win"`
	}
	type response struct {
		Data struct {
			Counters      []counter  `json:"counters"`
			Runes         []RunePage `json:"runes"`
			Tip           string     `json:"opponent_champion_tip"`
			LaneAdvantage string     `json:"lane_advantage_champion"`
			PlayStyle     string     `json:"recommended_play_style"`
		} `json:"data"`
	}
	var resp response
	me, opp := strings.ToUpper(normalize(key)), strings.ToUpper(normalize(opponent))
	fr, err := s.get(ctx, request{
		key:    "matchup/" + me + "/" + opp + "/" + position,
		tool:   "lol_get_lane_matchup_guide",
		args:   map[string]any{"position": position, "my_champion": me, "opponent_champion": opp},
		maxAge: MatchupMaxAge,
		decode: func(body string) (string, error) {
			resp = response{}
			if err := opgg.Decode(body, &resp); err != nil {
				return "", err
			}
			if len(resp.Data.Counters) == 0 {
				return "", ErrNoData
			}
			return "", nil
		},
	})
	if err != nil {
		return Matchup{}, err
	}
	m := Matchup{
		Freshness: fr, Key: key, Opponent: opponent, Position: position,
		Runes: resp.Data.Runes, Tip: resp.Data.Tip, LaneAdvantage: resp.Data.LaneAdvantage, PlayStyle: resp.Data.PlayStyle,
	}
	champs, cerr := s.Champions(ctx)
	if cerr != nil {
		champs = newChampions(nil)
	}
	for _, c := range resp.Data.Counters {
		m.Table = append(m.Table, Counter{Name: c.Name, Key: champs.KeyFor(c.Name), Play: c.Play, Win: c.Win})
	}
	return m, nil
}

// styleNames are Riot's rune trees by id. They haven't changed since runes
// reforged; an unknown id shows as a number.
var styleNames = map[int]string{8000: "Precision", 8100: "Domination", 8200: "Sorcery", 8300: "Inspiration", 8400: "Resolve"}

// statModNames are the stat shards by id.
var statModNames = map[int]string{
	5001: "Health Scaling", 5005: "Attack Speed", 5007: "Ability Haste", 5008: "Adaptive Force",
	5010: "Move Speed", 5011: "Health", 5013: "Tenacity and Slow Resist",
}

// String describes the page in one line, e.g. "Sorcery: Stormraider's
// Surge, Manaflow Band, Transcendence, Scorch / Resolve: Unflinching,
// Second Wind / Adaptive Force, Adaptive Force, Health". Ids without a name
// show as numbers.
func (r RunePage) String() string {
	names := func(ids []int, names []string) string {
		out := make([]string, len(ids))
		for i, id := range ids {
			if i < len(names) && names[i] != "" {
				out[i] = names[i]
			} else {
				out[i] = fmt.Sprint(id)
			}
		}
		return strings.Join(out, ", ")
	}
	style := func(id int) string {
		if n, ok := styleNames[id]; ok {
			return n
		}
		return fmt.Sprint(id)
	}
	mods := make([]string, len(r.StatMods))
	for i, id := range r.StatMods {
		if n, ok := statModNames[id]; ok {
			mods[i] = n
		} else {
			mods[i] = fmt.Sprint(id)
		}
	}
	return fmt.Sprintf("%s: %s / %s: %s / %s", style(r.PrimaryStyle), names(r.Primary, r.PrimaryNames),
		style(r.SubStyle), names(r.Sub, r.SubNames), strings.Join(mods, ", "))
}
