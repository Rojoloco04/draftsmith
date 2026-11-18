package analysis

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/Rojoloco04/lolstats/internal/riot"
	"github.com/Rojoloco04/lolstats/internal/store"
)

// LoadGames parses every archived match for puuid, oldest first.
func LoadGames(s *store.Store, puuid string, queues map[int]bool) ([]Game, error) {
	ids, err := s.MatchIDs()
	if err != nil {
		return nil, err
	}
	var games []Game
	for _, id := range ids {
		raw, err := s.LoadMatch(id)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", id, err)
		}
		var m riot.Match
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("%s: %w", id, err)
		}
		if len(queues) > 0 && !queues[m.Info.QueueID] {
			continue
		}
		var tl *riot.Timeline
		if traw, err := s.LoadTimeline(id); err == nil {
			tl = &riot.Timeline{}
			if err := json.Unmarshal(traw, tl); err != nil {
				return nil, fmt.Errorf("%s timeline: %w", id, err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%s timeline: %w", id, err)
		}
		if g, ok := Extract(&m, tl, puuid); ok {
			games = append(games, g)
		}
	}
	sort.Slice(games, func(i, j int) bool { return games[i].Start.Before(games[j].Start) })
	return games, nil
}
