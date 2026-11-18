package riot

// Only the fields draftsmith reads are declared. The raw JSON is archived in
// full, so adding a field later just means adding it here.

type Match struct {
	Metadata struct {
		MatchID      string   `json:"matchId"`
		Participants []string `json:"participants"`
	} `json:"metadata"`
	Info MatchInfo `json:"info"`
}

type MatchInfo struct {
	GameCreation       int64         `json:"gameCreation"`
	GameStartTimestamp int64         `json:"gameStartTimestamp"`
	GameDuration       int64         `json:"gameDuration"` // seconds
	GameVersion        string        `json:"gameVersion"`
	QueueID            int           `json:"queueId"`
	PlatformID         string        `json:"platformId"`
	Participants       []Participant `json:"participants"`
	Teams              []Team        `json:"teams"`
}

type Participant struct {
	ParticipantID               int    `json:"participantId"`
	PUUID                       string `json:"puuid"`
	RiotIDGameName              string `json:"riotIdGameName"`
	RiotIDTagline               string `json:"riotIdTagline"`
	ChampionName                string `json:"championName"`
	ChampLevel                  int    `json:"champLevel"`
	TeamID                      int    `json:"teamId"`
	TeamPosition                string `json:"teamPosition"`
	IndividualPosition          string `json:"individualPosition"`
	Win                         bool   `json:"win"`
	Kills                       int    `json:"kills"`
	Deaths                      int    `json:"deaths"`
	Assists                     int    `json:"assists"`
	TotalMinionsKilled          int    `json:"totalMinionsKilled"`
	NeutralMinionsKilled        int    `json:"neutralMinionsKilled"`
	VisionScore                 int    `json:"visionScore"`
	VisionWardsBoughtInGame     int    `json:"visionWardsBoughtInGame"`
	TotalDamageDealtToChampions int    `json:"totalDamageDealtToChampions"`
	GoldEarned                  int    `json:"goldEarned"`
	GameEndedInEarlySurrender   bool   `json:"gameEndedInEarlySurrender"`
	Item0                       int    `json:"item0"`
	Item1                       int    `json:"item1"`
	Item2                       int    `json:"item2"`
	Item3                       int    `json:"item3"`
	Item4                       int    `json:"item4"`
	Item5                       int    `json:"item5"`
	Item6                       int    `json:"item6"` // trinket
}

// Items returns item0..item6 (0 for an empty slot); the last is the trinket.
func (p *Participant) Items() [7]int {
	return [7]int{p.Item0, p.Item1, p.Item2, p.Item3, p.Item4, p.Item5, p.Item6}
}

type Team struct {
	TeamID int  `json:"teamId"`
	Win    bool `json:"win"`
}

type Timeline struct {
	Metadata struct {
		MatchID string `json:"matchId"`
	} `json:"metadata"`
	Info struct {
		FrameInterval int64   `json:"frameInterval"`
		Frames        []Frame `json:"frames"`
		Participants  []struct {
			ParticipantID int    `json:"participantId"`
			PUUID         string `json:"puuid"`
		} `json:"participants"`
	} `json:"info"`
}

type Frame struct {
	Timestamp         int64                       `json:"timestamp"` // ms
	ParticipantFrames map[string]ParticipantFrame `json:"participantFrames"`
	Events            []Event                     `json:"events"`
}

type ParticipantFrame struct {
	ParticipantID       int      `json:"participantId"`
	TotalGold           int      `json:"totalGold"`
	XP                  int      `json:"xp"`
	Level               int      `json:"level"`
	MinionsKilled       int      `json:"minionsKilled"`
	JungleMinionsKilled int      `json:"jungleMinionsKilled"`
	Position            Position `json:"position"`
}

type Position struct {
	X int `json:"x"`
	Y int `json:"y"`
}

type Event struct {
	Type                    string    `json:"type"`
	Timestamp               int64     `json:"timestamp"`
	KillerID                int       `json:"killerId"`
	VictimID                int       `json:"victimId"`
	AssistingParticipantIDs []int     `json:"assistingParticipantIds"`
	Position                *Position `json:"position,omitempty"`
}
