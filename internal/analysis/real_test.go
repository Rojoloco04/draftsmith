package analysis

import (
	"testing"

	"github.com/rojoloco04/draftsmith/internal/fixture"
)

// TestExtractRealGame pins the features of one real (scrubbed) ranked game.
// Expected values were cross-checked against an independent computation from
// the raw JSON.
func TestExtractRealGame(t *testing.T) {
	m, tl := fixture.Real()
	if m.Metadata.MatchID != fixture.RealMatchID || tl.Metadata.MatchID != fixture.RealMatchID {
		t.Fatalf("match ids: %q %q", m.Metadata.MatchID, tl.Metadata.MatchID)
	}
	if len(m.Info.Participants) != 10 || len(tl.Info.Frames) < 16 {
		t.Fatalf("participants=%d frames=%d", len(m.Info.Participants), len(tl.Info.Frames))
	}

	g, ok := Extract(&m, &tl, fixture.MePUUID)
	if !ok {
		t.Fatal("tracked player not found")
	}
	if g.Champion != "Vladimir" || g.Role != "MIDDLE" || g.Opponent != "Yone" || g.Win {
		t.Errorf("basic fields: champion=%s role=%s opponent=%s win=%v", g.Champion, g.Role, g.Opponent, g.Win)
	}
	if g.Patch != "16.22" || g.Queue != 420 || g.Remake {
		t.Errorf("patch=%q queue=%d remake=%v", g.Patch, g.Queue, g.Remake)
	}
	if g.Kills != 4 || g.Deaths != 7 || g.Assists != 10 || g.CS != 217 || g.ControlWards != 3 {
		t.Errorf("kda=%d/%d/%d cs=%d wards=%d", g.Kills, g.Deaths, g.Assists, g.CS, g.ControlWards)
	}
	if abs(g.Minutes-1771.0/60) > 1e-9 {
		t.Errorf("minutes = %v", g.Minutes)
	}
	if abs(g.KillParticipation-14.0/36) > 1e-9 {
		t.Errorf("kp = %v", g.KillParticipation)
	}
	if !g.HasTimeline || g.At10 == nil || g.At15 == nil || !g.At10.HasOpp {
		t.Fatalf("lane diffs missing: %+v", g)
	}
	if g.At10.GoldDiff != -133 || g.At10.CSDiff != 8 || g.At10.XPDiff != 264 {
		t.Errorf("at10 = %+v", *g.At10)
	}
	if g.At15.GoldDiff != -788 || g.At15.CSDiff != -3 || g.At15.XPDiff != -281 {
		t.Errorf("at15 = %+v", *g.At15)
	}
	if g.DeathsBefore15 != 2 || g.DeathsEnemySide != 2 || len(g.DeathSpots) != g.Deaths {
		t.Errorf("deaths: before15=%d enemySide=%d spots=%d", g.DeathsBefore15, g.DeathsEnemySide, len(g.DeathSpots))
	}
}
