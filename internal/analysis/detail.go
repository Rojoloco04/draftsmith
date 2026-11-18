package analysis

import "github.com/rojoloco04/draftsmith/internal/riot"

// PlayerLine is one player's end-of-game line for the match detail page.
// It deliberately has no name, Riot ID, or PUUID field: other players are
// only ever shown as champion and position.
type PlayerLine struct {
	ParticipantID int    `json:"participantId"`
	TeamID        int    `json:"teamId"`
	Position      string `json:"position"`
	Champion      string `json:"champion"`
	Me            bool   `json:"me"`
	Win           bool   `json:"win"`
	Level         int    `json:"level"`
	Kills         int    `json:"kills"`
	Deaths        int    `json:"deaths"`
	Assists       int    `json:"assists"`
	CS            int    `json:"cs"`
	Gold          int    `json:"gold"`
	Damage        int    `json:"damage"`
	Vision        int    `json:"vision"`
	// Items are item0..item6; 0 is an empty slot and the last is the trinket.
	Items [7]int `json:"items"`
	// Spells are the two summoner spell ids (0 if unknown).
	Spells [2]int `json:"spells"`
	Runes  Runes  `json:"runes"`
}

// Runes is a rune page as ids. Zero values mean unknown.
type Runes struct {
	PrimaryStyle int    `json:"primaryStyle"`
	Primary      [4]int `json:"primary"` // keystone first
	SubStyle     int    `json:"subStyle"`
	Sub          [2]int `json:"sub"`
	Shards       [3]int `json:"shards"` // offense, flex, defense
}

// Keystone is the page's first primary rune.
func (r Runes) Keystone() int { return r.Primary[0] }

// RunesOf reads a participant's rune page.
func RunesOf(p *riot.Participant) Runes {
	r := Runes{Shards: [3]int{p.Perks.StatPerks.Offense, p.Perks.StatPerks.Flex, p.Perks.StatPerks.Defense}}
	for i, st := range p.Perks.Styles {
		switch {
		case st.Description == "primaryStyle" || (st.Description == "" && i == 0):
			r.PrimaryStyle = st.Style
			for j := 0; j < len(st.Selections) && j < len(r.Primary); j++ {
				r.Primary[j] = st.Selections[j].Perk
			}
		case st.Description == "subStyle" || (st.Description == "" && i == 1):
			r.SubStyle = st.Style
			for j := 0; j < len(st.Selections) && j < len(r.Sub); j++ {
				r.Sub[j] = st.Selections[j].Perk
			}
		}
	}
	return r
}

// LanePoint is my and my lane opponent's totals at one timeline frame.
type LanePoint struct {
	Minute  float64 `json:"minute"`
	Gold    int     `json:"gold"`
	CS      int     `json:"cs"`
	XP      int     `json:"xp"`
	HasOpp  bool    `json:"hasOpp"`
	OppGold int     `json:"oppGold"`
	OppCS   int     `json:"oppCs"`
	OppXP   int     `json:"oppXp"`
}

// Detail is what the match detail page shows beyond Game.
type Detail struct {
	Players []PlayerLine
	Lane    []LanePoint // empty without a timeline
}

// ExtractDetail pulls every player's line and the lane curve from a match.
// ok is false if puuid is not in the match. tl may be nil.
func ExtractDetail(m *riot.Match, tl *riot.Timeline, puuid string) (d Detail, ok bool) {
	me, opp := meAndOpponent(m, puuid)
	if me == nil {
		return d, false
	}
	for i := range m.Info.Participants {
		p := &m.Info.Participants[i]
		pos := p.TeamPosition
		if pos == "" {
			pos = p.IndividualPosition
		}
		d.Players = append(d.Players, PlayerLine{
			ParticipantID: p.ParticipantID,
			TeamID:        p.TeamID,
			Position:      pos,
			Champion:      p.ChampionName,
			Me:            p == me,
			Win:           p.Win,
			Level:         p.ChampLevel,
			Kills:         p.Kills,
			Deaths:        p.Deaths,
			Assists:       p.Assists,
			CS:            p.TotalMinionsKilled + p.NeutralMinionsKilled,
			Gold:          p.GoldEarned,
			Damage:        p.TotalDamageDealtToChampions,
			Vision:        p.VisionScore,
			Items:         p.Items(),
			Spells:        [2]int{p.Summoner1ID, p.Summoner2ID},
			Runes:         RunesOf(p),
		})
	}
	if tl == nil {
		return d, true
	}
	for i := range tl.Info.Frames {
		f := &tl.Info.Frames[i]
		mine, ok := frameFor(f, me.ParticipantID)
		if !ok {
			continue
		}
		pt := LanePoint{
			Minute: float64(f.Timestamp) / 60000,
			Gold:   mine.TotalGold,
			CS:     mine.MinionsKilled + mine.JungleMinionsKilled,
			XP:     mine.XP,
		}
		if opp != nil {
			if theirs, ok := frameFor(f, opp.ParticipantID); ok {
				pt.HasOpp = true
				pt.OppGold = theirs.TotalGold
				pt.OppCS = theirs.MinionsKilled + theirs.JungleMinionsKilled
				pt.OppXP = theirs.XP
			}
		}
		d.Lane = append(d.Lane, pt)
	}
	return d, true
}
