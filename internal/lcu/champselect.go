package lcu

import (
	"context"
	"encoding/json"
	"sort"
)

// Session is the part of /lol-champ-select/v1/session Draftsmith reads.
//
// It names only the fields the compliance rules allow (see PLAN.md, Phase
// 3): positions, champions, picks, bans and the timer. Decoding into it
// drops everything else, including every identity field the client sends
// for other players (summoner ids, PUUIDs, names), so those are never held,
// logged or stored. Add fields here only if they identify no one.
type Session struct {
	LocalPlayerCellID int        `json:"localPlayerCellId"`
	MyTeam            []Member   `json:"myTeam"`
	TheirTeam         []Member   `json:"theirTeam"`
	Actions           [][]Action `json:"actions"`
	Bans              Bans       `json:"bans"`
	Timer             Timer      `json:"timer"`
	IsCustomGame      bool       `json:"isCustomGame"`
}

// Member is one player's slot, by cell. ChampionID is a pick (or the
// current hover while the pick is in progress), ChampionPickIntent a
// declared intent before the pick turn; both are 0 when unset.
type Member struct {
	CellID             int    `json:"cellId"`
	Team               int    `json:"team"`
	AssignedPosition   string `json:"assignedPosition"` // "top", "jungle", "middle", "bottom", "utility", or "" (blind pick, enemies)
	ChampionID         int    `json:"championId"`
	ChampionPickIntent int    `json:"championPickIntent"`
}

// Action is one pick or ban turn.
type Action struct {
	ID           int    `json:"id"`
	ActorCellID  int    `json:"actorCellId"`
	ChampionID   int    `json:"championId"`
	Type         string `json:"type"` // "pick", "ban", "ten_bans_reveal", ...
	Completed    bool   `json:"completed"`
	IsInProgress bool   `json:"isInProgress"`
	IsAllyAction bool   `json:"isAllyAction"`
}

// Bans are the champion ids banned so far.
type Bans struct {
	MyTeamBans    []int `json:"myTeamBans"`
	TheirTeamBans []int `json:"theirTeamBans"`
	NumBans       int   `json:"numBans"`
}

// Timer is the current champ select phase and its time left.
type Timer struct {
	Phase                   string `json:"phase"` // PLANNING, BAN_PICK, FINALIZATION, GAME_STARTING
	AdjustedTimeLeftInPhase int64  `json:"adjustedTimeLeftInPhase"` // ms
	TotalTimeInPhase        int64  `json:"totalTimeInPhase"`        // ms
	InternalNowInEpochMs    int64  `json:"internalNowInEpochMs"`
}

// Me returns my own slot.
func (s Session) Me() (Member, bool) {
	for _, m := range s.MyTeam {
		if m.CellID == s.LocalPlayerCellID {
			return m, true
		}
	}
	return Member{}, false
}

// ChampSelectSession returns the current champ select, or ErrNotFound
// outside one.
func (c *Client) ChampSelectSession(ctx context.Context) (Session, error) {
	var s Session
	err := c.Get(ctx, "/lol-champ-select/v1/session", &s)
	return s, err
}

// ChampSelectShape is ChampSelectSession for checking field names against
// the real client: it returns the session plus every key path in the raw
// payload ("myTeam[].cellId"), with no values, so the shape can be checked
// without keeping anyone's identity.
func (c *Client) ChampSelectShape(ctx context.Context) (Session, []string, error) {
	var raw json.RawMessage
	if err := c.Get(ctx, "/lol-champ-select/v1/session", &raw); err != nil {
		return Session{}, nil, err
	}
	var s Session
	if err := json.Unmarshal(raw, &s); err != nil {
		return Session{}, nil, err
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return Session{}, nil, err
	}
	set := map[string]bool{}
	keyPaths(v, "", set)
	paths := make([]string, 0, len(set))
	for p := range set {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return s, paths, nil
}

func keyPaths(v any, prefix string, out map[string]bool) {
	switch t := v.(type) {
	case map[string]any:
		for k, sub := range t {
			p := k
			if prefix != "" {
				p = prefix + "." + k
			}
			out[p] = true
			keyPaths(sub, p, out)
		}
	case []any:
		for _, sub := range t {
			keyPaths(sub, prefix+"[]", out)
		}
	}
}
