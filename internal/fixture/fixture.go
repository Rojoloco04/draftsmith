// Package fixture builds synthetic match-v5 and timeline JSON for tests,
// shaped like real Riot responses.
package fixture

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/rojoloco04/draftsmith/internal/riot"
)

const MePUUID = "me-puuid"

var positions = []string{"TOP", "JUNGLE", "MIDDLE", "BOTTOM", "UTILITY"}

// Spec controls the tracked player's game.
type Spec struct {
	ID                   string
	StartMs              int64
	Seconds              int64
	Champion             string
	Opponent             string
	Position             string // tracked player's teamPosition
	Win                  bool
	Kills                int
	Deaths               int
	Assists              int
	GoldLead             int // tracked player's gold lead over opponent at every frame
	CSLead               int
	EarlyDeathsEnemySide int    // deaths at minute 5.. on enemy half
	Queue                int    // default solo queue
	NoTimeline           bool   // Archive stores no timeline for this game
	PUUID                string // tracked player's PUUID, default MePUUID (another of my accounts)
}

func (s *Spec) defaults() {
	if s.Seconds == 0 {
		s.Seconds = 1800
	}
	if s.Position == "" {
		s.Position = "MIDDLE"
	}
	if s.Queue == 0 {
		s.Queue = riot.QueueSoloDuo
	}
	if s.PUUID == "" {
		s.PUUID = MePUUID
	}
}

// Match returns a match where the tracked player is participant 1-5 on blue
// side (team 100) and the opponent is the same position on red.
func Match(s Spec) riot.Match {
	s.defaults()
	var m riot.Match
	m.Metadata.MatchID = s.ID
	m.Info.GameCreation = s.StartMs - 60000
	m.Info.GameStartTimestamp = s.StartMs
	m.Info.GameDuration = s.Seconds
	m.Info.GameVersion = "15.22.712.1234"
	m.Info.QueueID = s.Queue
	m.Info.PlatformID = "NA1"
	for team := 0; team < 2; team++ {
		for i, pos := range positions {
			pid := team*5 + i + 1
			p := riot.Participant{
				ParticipantID:               pid,
				PUUID:                       fmt.Sprintf("p%d", pid),
				ChampionName:                fmt.Sprintf("Champ%d", pid),
				TeamID:                      100 + team*100,
				TeamPosition:                pos,
				IndividualPosition:          pos,
				Win:                         (team == 0) == s.Win,
				Kills:                       2,
				Deaths:                      2,
				Assists:                     4,
				TotalMinionsKilled:          150,
				VisionScore:                 20,
				TotalDamageDealtToChampions: 15000,
				GoldEarned:                  10000,
				ChampLevel:                  14,
				Item0:                       1055,
				Item1:                       3006,
				Item6:                       3340,
			}
			if team == 0 && pos == s.Position {
				p.PUUID = s.PUUID
				p.ChampionName = s.Champion
				p.Kills, p.Deaths, p.Assists = s.Kills, s.Deaths, s.Assists
				p.TotalMinionsKilled = 200
				p.NeutralMinionsKilled = 10
				p.VisionScore = 30
				p.VisionWardsBoughtInGame = 3
				p.TotalDamageDealtToChampions = 20000
				p.GoldEarned = 12000
			}
			if team == 1 && pos == s.Position && s.Opponent != "" {
				p.ChampionName = s.Opponent
			}
			m.Metadata.Participants = append(m.Metadata.Participants, p.PUUID)
			m.Info.Participants = append(m.Info.Participants, p)
		}
	}
	m.Info.Teams = []riot.Team{{TeamID: 100, Win: s.Win}, {TeamID: 200, Win: !s.Win}}
	return m
}

// Timeline returns one frame per minute with linear gold/cs growth and the
// requested leads for the tracked player.
func Timeline(s Spec) riot.Timeline {
	s.defaults()
	m := Match(s)
	var tl riot.Timeline
	tl.Metadata.MatchID = s.ID
	tl.Info.FrameInterval = 60000
	me, opp := 0, 0
	for _, p := range m.Info.Participants {
		tl.Info.Participants = append(tl.Info.Participants, struct {
			ParticipantID int    `json:"participantId"`
			PUUID         string `json:"puuid"`
		}{p.ParticipantID, p.PUUID})
		if p.PUUID == s.PUUID {
			me = p.ParticipantID
		}
	}
	opp = me + 5
	minutes := int(s.Seconds / 60)
	for min := 0; min <= minutes; min++ {
		f := riot.Frame{Timestamp: int64(min)*60000 + int64(min%3)*7, ParticipantFrames: map[string]riot.ParticipantFrame{}}
		for pid := 1; pid <= 10; pid++ {
			pf := riot.ParticipantFrame{
				ParticipantID: pid,
				TotalGold:     500 + 400*min,
				XP:            450 * min,
				MinionsKilled: 7 * min,
				Level:         1 + min/2,
			}
			if pid == me {
				pf.TotalGold += s.GoldLead
				pf.MinionsKilled += s.CSLead
			}
			f.ParticipantFrames[strconv.Itoa(pid)] = pf
		}
		tl.Info.Frames = append(tl.Info.Frames, f)
	}
	// Deaths for the tracked player: the first EarlyDeathsEnemySide happen on
	// the red half before 15, the rest late on the blue half.
	for i := 0; i < s.Deaths; i++ {
		min := 20 + i
		pos := riot.Position{X: 3000, Y: 3000}
		if i < s.EarlyDeathsEnemySide {
			min = 5 + i
			pos = riot.Position{X: 11000, Y: 11000}
		}
		if min >= len(tl.Info.Frames) {
			min = len(tl.Info.Frames) - 1
		}
		ev := riot.Event{Type: "CHAMPION_KILL", Timestamp: int64(min)*60000 + 1000, KillerID: opp, VictimID: me, Position: &pos}
		tl.Info.Frames[min].Events = append(tl.Info.Frames[min].Events, ev)
	}
	return tl
}

func MustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
