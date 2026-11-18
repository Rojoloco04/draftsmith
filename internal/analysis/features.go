// Package analysis turns archived matches into per-game features and
// aggregates them into a report.
package analysis

import (
	"strings"
	"time"

	"github.com/rojoloco04/draftsmith/internal/riot"
)

// Game is one ranked game from the tracked player's point of view.
type Game struct {
	MatchID           string    `json:"matchId"`
	Start             time.Time `json:"start"`
	Patch             string    `json:"patch"`
	Queue             int       `json:"queue"`
	Remake            bool      `json:"remake"`
	Minutes           float64   `json:"minutes"`
	Champion          string    `json:"champion"`
	Role              string    `json:"role"`
	Opponent          string    `json:"opponent,omitempty"`
	Win               bool      `json:"win"`
	Kills             int       `json:"kills"`
	Deaths            int       `json:"deaths"`
	Assists           int       `json:"assists"`
	CS                int       `json:"cs"`
	CSPerMin          float64   `json:"csPerMin"`
	GoldPerMin        float64   `json:"goldPerMin"`
	VisionPerMin      float64   `json:"visionPerMin"`
	ControlWards      int       `json:"controlWards"`
	KillParticipation float64   `json:"killParticipation"`
	DamageShare       float64   `json:"damageShare"`

	// Timeline features. Nil when the timeline aged out (Riot keeps them
	// about a year) or the game ended before the mark.
	At10            *LaneDiff `json:"at10,omitempty"`
	At15            *LaneDiff `json:"at15,omitempty"`
	HasTimeline     bool      `json:"hasTimeline"`
	DeathsBefore15  int       `json:"deathsBefore15"`
	DeathsEnemySide int       `json:"deathsEnemySide"`
	DeathSpots      []Death   `json:"deathSpots,omitempty"`
}

// LaneDiff is the player's numbers at a minute mark, and the difference to
// the lane opponent (same position on the enemy team).
type LaneDiff struct {
	Gold     int  `json:"gold"`
	CS       int  `json:"cs"`
	XP       int  `json:"xp"`
	GoldDiff int  `json:"goldDiff"`
	CSDiff   int  `json:"csDiff"`
	XPDiff   int  `json:"xpDiff"`
	HasOpp   bool `json:"hasOpp"`
}

type Death struct {
	Minute float64 `json:"minute"`
	X      int     `json:"x"`
	Y      int     `json:"y"`
}

// mapEdge is roughly the far corner of Summoner's Rift in game units.
// Blue (team 100) spawns bottom left; the diagonal x+y = mapEdge splits it.
const mapEdge = 14870

// Extract computes features for puuid. ok is false if the player is not in
// the match. tl may be nil.
func Extract(m *riot.Match, tl *riot.Timeline, puuid string) (g Game, ok bool) {
	me, opp := meAndOpponent(m, puuid)
	if me == nil {
		return g, false
	}

	secs := m.Info.GameDuration
	if secs > 100000 { // pre 11.20 matches reported milliseconds
		secs /= 1000
	}
	start := m.Info.GameStartTimestamp
	if start == 0 {
		start = m.Info.GameCreation
	}

	g = Game{
		MatchID:      m.Metadata.MatchID,
		Start:        time.UnixMilli(start),
		Patch:        patch(m.Info.GameVersion),
		Queue:        m.Info.QueueID,
		Remake:       secs < 300 || me.GameEndedInEarlySurrender,
		Minutes:      float64(secs) / 60,
		Champion:     me.ChampionName,
		Role:         me.TeamPosition,
		Win:          me.Win,
		Kills:        me.Kills,
		Deaths:       me.Deaths,
		Assists:      me.Assists,
		CS:           me.TotalMinionsKilled + me.NeutralMinionsKilled,
		ControlWards: me.VisionWardsBoughtInGame,
	}
	if g.Role == "" {
		g.Role = me.IndividualPosition
	}

	teamKills, teamDmg := 0, 0
	for i := range m.Info.Participants {
		p := &m.Info.Participants[i]
		if p.TeamID == me.TeamID {
			teamKills += p.Kills
			teamDmg += p.TotalDamageDealtToChampions
		}
	}
	if opp != nil {
		g.Opponent = opp.ChampionName
	}
	if g.Minutes > 0 {
		g.CSPerMin = float64(g.CS) / g.Minutes
		g.GoldPerMin = float64(me.GoldEarned) / g.Minutes
		g.VisionPerMin = float64(me.VisionScore) / g.Minutes
	}
	if teamKills > 0 {
		g.KillParticipation = float64(me.Kills+me.Assists) / float64(teamKills)
	}
	if teamDmg > 0 {
		g.DamageShare = float64(me.TotalDamageDealtToChampions) / float64(teamDmg)
	}

	if tl != nil && len(tl.Info.Frames) > 0 {
		g.HasTimeline = true
		myID := me.ParticipantID
		oppID := 0
		if opp != nil {
			oppID = opp.ParticipantID
		}
		g.At10 = laneAt(tl, 10, myID, oppID)
		g.At15 = laneAt(tl, 15, myID, oppID)
		for _, f := range tl.Info.Frames {
			for _, e := range f.Events {
				if e.Type != "CHAMPION_KILL" || e.VictimID != myID {
					continue
				}
				d := Death{Minute: float64(e.Timestamp) / 60000}
				if e.Position != nil {
					d.X, d.Y = e.Position.X, e.Position.Y
					if onEnemySide(me.TeamID, d.X, d.Y) {
						g.DeathsEnemySide++
					}
				}
				if d.Minute < 15 {
					g.DeathsBefore15++
				}
				g.DeathSpots = append(g.DeathSpots, d)
			}
		}
	}
	return g, true
}

// meAndOpponent finds the tracked player and their lane opponent (same
// teamPosition on the other team). me is nil if puuid is not in the match;
// opp is nil if there is no clear opponent.
func meAndOpponent(m *riot.Match, puuid string) (me, opp *riot.Participant) {
	for i := range m.Info.Participants {
		if m.Info.Participants[i].PUUID == puuid {
			me = &m.Info.Participants[i]
		}
	}
	if me == nil || me.TeamPosition == "" {
		return me, nil
	}
	for i := range m.Info.Participants {
		p := &m.Info.Participants[i]
		if p.TeamID != me.TeamID && p.TeamPosition == me.TeamPosition {
			opp = p
		}
	}
	return me, opp
}

func laneAt(tl *riot.Timeline, minute int, myID, oppID int) *LaneDiff {
	target := int64(minute) * 60000
	var best *riot.Frame
	for i := range tl.Info.Frames {
		f := &tl.Info.Frames[i]
		if abs64(f.Timestamp-target) <= 30000 {
			if best == nil || abs64(f.Timestamp-target) < abs64(best.Timestamp-target) {
				best = f
			}
		}
	}
	if best == nil {
		return nil
	}
	mine, ok := frameFor(best, myID)
	if !ok {
		return nil
	}
	d := &LaneDiff{
		Gold: mine.TotalGold,
		CS:   mine.MinionsKilled + mine.JungleMinionsKilled,
		XP:   mine.XP,
	}
	if theirs, ok := frameFor(best, oppID); ok && oppID != 0 {
		d.HasOpp = true
		d.GoldDiff = d.Gold - theirs.TotalGold
		d.CSDiff = d.CS - (theirs.MinionsKilled + theirs.JungleMinionsKilled)
		d.XPDiff = d.XP - theirs.XP
	}
	return d
}

func frameFor(f *riot.Frame, id int) (riot.ParticipantFrame, bool) {
	for _, pf := range f.ParticipantFrames {
		if pf.ParticipantID == id {
			return pf, true
		}
	}
	return riot.ParticipantFrame{}, false
}

func onEnemySide(teamID, x, y int) bool {
	if teamID == 100 {
		return x+y > mapEdge
	}
	return x+y < mapEdge
}

func patch(v string) string {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return v
	}
	return parts[0] + "." + parts[1]
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
