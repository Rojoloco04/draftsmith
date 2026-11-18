package fixture

import (
	"time"

	"github.com/rojoloco04/draftsmith/internal/riot"
	"github.com/rojoloco04/draftsmith/internal/store"
)

// Archive writes a synthetic match and timeline for each spec into s, plus
// the scrubbed real game if withReal is set, and records MePUUID as the
// tracked player. Specs with NoTimeline get a match only.
func Archive(s *store.Store, specs []Spec, withReal bool) error {
	for _, sp := range specs {
		if err := s.PutMatch(sp.ID, MustJSON(Match(sp))); err != nil {
			return err
		}
		if sp.NoTimeline {
			continue
		}
		if err := s.PutTimeline(sp.ID, MustJSON(Timeline(sp))); err != nil {
			return err
		}
	}
	if withReal {
		if err := s.PutMatch(RealMatchID, RealMatchJSON()); err != nil {
			return err
		}
		if err := s.PutTimeline(RealMatchID, RealTimelineJSON()); err != nil {
			return err
		}
	}
	st, err := s.LoadState()
	if err != nil {
		return err
	}
	st.PUUID = MePUUID
	st.RiotID = "Tester#NA1"
	st.Platform = "na1"
	return s.SaveState(st)
}

// RankSnapshot writes one day's standings into s.
func RankSnapshot(s *store.Store, day time.Time, tier, division string, lp, wins, losses int) error {
	return s.PutRankSnapshot(day, []riot.LeagueEntry{{
		QueueType: "RANKED_SOLO_5x5", Tier: tier, Rank: division, LeaguePoints: lp, Wins: wins, Losses: losses,
	}})
}
