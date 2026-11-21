package meta

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/rojoloco04/draftsmith/internal/opgg"
)

// Champion is one row of OP.GG's champion table.
type Champion struct {
	ID   int    `json:"champion_id"`
	Key  string `json:"key"`  // Riot's championName, e.g. MonkeyKing
	Name string `json:"name"` // display name, e.g. Wukong
}

// Champions joins OP.GG's display names to Riot's keys.
type Champions struct {
	Freshness
	List   []Champion
	byName map[string]Champion
	byKey  map[string]Champion
}

func newChampions(list []Champion) Champions {
	c := Champions{byName: map[string]Champion{}, byKey: map[string]Champion{}}
	for _, ch := range list {
		// The table also has mode variants (id 60001+, key "Jade_Annie")
		// sharing display names with the real champions. Riot keys never
		// contain an underscore.
		if strings.Contains(ch.Key, "_") {
			continue
		}
		c.List = append(c.List, ch)
		c.byName[normalize(ch.Name)] = ch
		c.byKey[normalize(ch.Key)] = ch
	}
	return c
}

// KeyFor returns Riot's key for an OP.GG display name. Without a match (or
// without the table, if it couldn't be fetched) it falls back to the name
// with everything but letters and digits removed, which is right for most
// champions ("Twisted Fate" -> "TwistedFate").
func (c Champions) KeyFor(name string) string {
	if ch, ok := c.byName[normalize(name)]; ok {
		return ch.Key
	}
	if ch, ok := c.byKey[normalize(name)]; ok {
		return ch.Key
	}
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Champions returns OP.GG's champion table.
func (s *Service) Champions(ctx context.Context) (Champions, error) {
	var out Champions
	fr, err := s.get(ctx, request{
		key:    "champions",
		tool:   "lol_list_champions",
		args:   map[string]any{"desired_output_fields": []string{"data.champions[].{champion_id,key,name}"}},
		maxAge: ChampionsMaxAge,
		decode: func(body string) (string, error) {
			var resp struct {
				Data struct {
					Champions []Champion `json:"champions"`
				} `json:"data"`
			}
			if err := opgg.Decode(body, &resp); err != nil {
				return "", err
			}
			if len(resp.Data.Champions) < 100 {
				return "", fmt.Errorf("only %d champions listed", len(resp.Data.Champions))
			}
			out = newChampions(resp.Data.Champions)
			return "", nil
		},
	})
	if err != nil {
		return Champions{}, err
	}
	out.Freshness = fr
	return out, nil
}

// TierEntry is one champion in one position of the tier list.
type TierEntry struct {
	Name     string // OP.GG display name
	Key      string // Riot championName
	Position string
	Play     int
	Win      int
	PickRate float64
	BanRate  float64
	// RoleRate is the share of the champion's games played in this
	// position (OP.GG's role_rate, 2 decimals). 0 in tier lists cached
	// before it was requested.
	RoleRate float64
	Tier     int // 1 (OP) to 5 (weak)
	Rank     int // OP.GG's rank within the position
}

// WinRate is win/play. OP.GG's own win_rate is rounded to 2 decimals,
// which is too coarse to rank by.
func (e TierEntry) WinRate() float64 {
	if e.Play == 0 {
		return math.NaN()
	}
	return float64(e.Win) / float64(e.Play)
}

// TierList is OP.GG's lane meta for every position (Emerald+, current
// patch, no filters available).
type TierList struct {
	Freshness
	// Entries is every position in Positions order, each in OP.GG rank
	// order.
	Entries []TierEntry
	// ChampionsErr is set if the champion table couldn't be fetched, in
	// which case keys are guessed from display names.
	ChampionsErr error
}

// Position returns one position's entries in OP.GG rank order.
func (t TierList) Position(p string) []TierEntry {
	var out []TierEntry
	for _, e := range t.Entries {
		if e.Position == p {
			out = append(out, e)
		}
	}
	return out
}

// Find returns a champion's entry in a position. key is Riot's
// championName; matching ignores case, so FiddleSticks finds Fiddlesticks.
func (t TierList) Find(key, position string) (TierEntry, bool) {
	k := normalize(key)
	for _, e := range t.Entries {
		if e.Position == position && normalize(e.Key) == k {
			return e, true
		}
	}
	return TierEntry{}, false
}

// TierList returns the lane meta for all five positions in one call.
func (s *Service) TierList(ctx context.Context) (TierList, error) {
	type row struct {
		Champion string  `json:"champion"`
		Play     int     `json:"play"`
		Win      int     `json:"win"`
		PickRate float64 `json:"pick_rate"`
		BanRate  float64 `json:"ban_rate"`
		RoleRate float64 `json:"role_rate"`
		Tier     int     `json:"tier"`
		Rank     int     `json:"rank"`
	}
	var rows map[string][]row
	fr, err := s.get(ctx, request{
		key:  "tierlist",
		tool: "lol_list_lane_meta_champions",
		args: map[string]any{
			"position":              "all",
			"desired_output_fields": []string{"data.positions.{" + strings.Join(Positions, ",") + "}[].{champion,play,win,pick_rate,role_rate,ban_rate,tier,rank}"},
		},
		maxAge: TierListMaxAge,
		decode: func(body string) (string, error) {
			var resp struct {
				Data struct {
					Positions map[string][]row `json:"positions"`
				} `json:"data"`
			}
			if err := opgg.Decode(body, &resp); err != nil {
				return "", err
			}
			for _, p := range Positions {
				if len(resp.Data.Positions[p]) == 0 {
					return "", fmt.Errorf("tier list has no %s champions", p)
				}
			}
			rows = resp.Data.Positions
			return "", nil
		},
	})
	if err != nil {
		return TierList{}, err
	}
	out := TierList{Freshness: fr}
	champs, cerr := s.Champions(ctx)
	if cerr != nil {
		out.ChampionsErr = cerr
		champs = newChampions(nil)
	}
	for _, p := range Positions {
		for _, r := range rows[p] {
			out.Entries = append(out.Entries, TierEntry{
				Name: r.Champion, Key: champs.KeyFor(r.Champion), Position: p,
				Play: r.Play, Win: r.Win, PickRate: r.PickRate, BanRate: r.BanRate, RoleRate: r.RoleRate, Tier: r.Tier, Rank: r.Rank,
			})
		}
	}
	return out, nil
}

// Counter is a lane opponent from a champion's analysis. Win counts the
// analyzed champion's wins in both counter lists (checked against
// my_win_rate; see docs/opgg-tools.md).
type Counter struct {
	Name string
	Key  string
	Play int
	Win  int
}

// WinRate is the analyzed champion's win rate into this opponent.
func (c Counter) WinRate() float64 {
	if c.Play == 0 {
		return math.NaN()
	}
	return float64(c.Win) / float64(c.Play)
}

// RunePage is OP.GG's most played rune page: the ids an LCU rune page
// needs.
type RunePage struct {
	PrimaryStyle int      `json:"primary_page_id"`
	Primary      []int    `json:"primary_rune_ids"`
	PrimaryNames []string `json:"primary_rune_names"`
	SubStyle     int      `json:"secondary_page_id"`
	Sub          []int    `json:"secondary_rune_ids"`
	SubNames     []string `json:"secondary_rune_names"`
	StatMods     []int    `json:"stat_mod_ids"`
	Play         int      `json:"play"`
	Win          int      `json:"win"`
	PickRate     float64  `json:"pick_rate"`
}

// Analysis is OP.GG's view of one champion in one position.
type Analysis struct {
	Freshness
	Key      string // Riot championName, as asked for
	Position string
	Bracket  string

	Play       int
	WinRate    float64 // rounded to 2 decimals by OP.GG
	PickRate   float64
	BanRate    float64
	Tier       int
	Rank       int
	DamageType string // AD, AP, BOTH
	Runes      RunePage
	// Build is the most played summoner spells and items.
	Build Build
	// Good are the opponents this champion does best into, Bad the ones
	// that counter it (OP.GG lists three of each).
	Good []Counter
	Bad  []Counter
}

// ErrNoData means OP.GG has no stats for that champion in that position.
var ErrNoData = errors.New("OP.GG has no data for that champion and position")

// analysisVersion is part of the analysis cache key. Bump it when the
// requested fields change, so copies cached without them are fetched again
// rather than served for a day. v2 added rune names, v3 spells and
// items, v4 item names and skill orders, v5 the 4th to 6th and late
// item options.
const analysisVersion = "v5"

// Analysis returns OP.GG's stats, counters and runes for a champion (Riot
// key) in a position, for the Emerald+ bracket.
func (s *Service) Analysis(ctx context.Context, key, position string) (Analysis, error) {
	if Role(position) == "" {
		return Analysis{}, fmt.Errorf("unknown position %q", position)
	}
	type counter struct {
		Name string `json:"champion_name"`
		Play int    `json:"play"`
		Win  int    `json:"win"`
	}
	type response struct {
		Data struct {
			Summary struct {
				Stats struct {
					Play     int     `json:"play"`
					WinRate  float64 `json:"win_rate"`
					PickRate float64 `json:"pick_rate"`
					BanRate  float64 `json:"ban_rate"`
					Tier     int     `json:"tier"`
					Rank     int     `json:"rank"`
				} `json:"average_stats"`
			} `json:"summary"`
			analysisBuild
			DamageType string    `json:"damage_type"`
			Strong     []counter `json:"strong_counters"`
			Weak       []counter `json:"weak_counters"`
			Runes      RunePage  `json:"runes"`
			Trends     struct {
				Win struct {
					Version string `json:"version"`
				} `json:"win"`
			} `json:"trends"`
		} `json:"data"`
	}
	var resp response
	champ := strings.ToUpper(normalize(key))
	fr, err := s.get(ctx, request{
		key:  "analysis/" + champ + "/" + position + "/" + Bracket + "/" + analysisVersion,
		tool: "lol_get_champion_analysis",
		args: map[string]any{
			"game_mode": "ranked", "champion": champ, "position": position, "tier": Bracket,
			"desired_output_fields": append([]string{
				"data.summary.average_stats.{play,win_rate,pick_rate,ban_rate,tier,rank}",
				"data.runes.{primary_page_id,primary_rune_ids[],primary_rune_names[],secondary_page_id,secondary_rune_ids[],secondary_rune_names[],stat_mod_ids[],play,win,pick_rate}",
				"data.strong_counters[].{champion_name,play,win}",
				"data.weak_counters[].{champion_name,play,win}",
				"data.damage_type",
				"data.trends.win.{version}",
			}, buildFields...),
		},
		maxAge: AnalysisMaxAge,
		decode: func(body string) (string, error) {
			resp = response{}
			if err := opgg.Decode(body, &resp); err != nil {
				return "", err
			}
			if resp.Data.Summary.Stats.Play == 0 {
				return "", ErrNoData
			}
			return resp.Data.Trends.Win.Version, nil
		},
	})
	if err != nil {
		return Analysis{}, err
	}
	d := resp.Data
	st := d.Summary.Stats
	a := Analysis{
		Freshness: fr, Key: key, Position: position, Bracket: Bracket,
		Play: st.Play, WinRate: st.WinRate, PickRate: st.PickRate, BanRate: st.BanRate, Tier: st.Tier, Rank: st.Rank,
		DamageType: d.DamageType, Runes: d.Runes, Build: d.build(),
	}
	champs, cerr := s.Champions(ctx)
	if cerr != nil {
		champs = newChampions(nil)
	}
	for _, c := range d.Strong {
		a.Good = append(a.Good, Counter{Name: c.Name, Key: champs.KeyFor(c.Name), Play: c.Play, Win: c.Win})
	}
	for _, c := range d.Weak {
		a.Bad = append(a.Bad, Counter{Name: c.Name, Key: champs.KeyFor(c.Name), Play: c.Play, Win: c.Win})
	}
	return a, nil
}
