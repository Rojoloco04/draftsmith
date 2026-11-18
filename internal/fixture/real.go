package fixture

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"io"

	"github.com/rojoloco04/draftsmith/internal/riot"
)

// A real ranked game (NA1_5681809843, patch 16.22) as returned by match-v5,
// with every PUUID, Riot ID, summoner name and summoner ID replaced. The
// tracked player's PUUID is MePUUID; everyone else is "scrubbed-puuid-N"
// where N is their participantId.
var (
	//go:embed testdata/match.json.gz
	realMatchGz []byte
	//go:embed testdata/timeline.json.gz
	realTimelineGz []byte
)

const RealMatchID = "NA1_5681809843"

// RealMatchJSON returns the scrubbed raw match JSON.
func RealMatchJSON() []byte { return gunzip(realMatchGz) }

// RealTimelineJSON returns the scrubbed raw timeline JSON.
func RealTimelineJSON() []byte { return gunzip(realTimelineGz) }

// Real returns the scrubbed match and timeline decoded into riot types.
func Real() (riot.Match, riot.Timeline) {
	var m riot.Match
	var tl riot.Timeline
	if err := json.Unmarshal(RealMatchJSON(), &m); err != nil {
		panic(err)
	}
	if err := json.Unmarshal(RealTimelineJSON(), &tl); err != nil {
		panic(err)
	}
	return m, tl
}

func gunzip(b []byte) []byte {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		panic(err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		panic(err)
	}
	return out
}
