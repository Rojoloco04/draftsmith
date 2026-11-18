package draft

import (
	"context"
	"math"
	"os"
	"testing"
	"time"

	"github.com/rojoloco04/draftsmith/internal/db"
	"github.com/rojoloco04/draftsmith/internal/meta"
	"github.com/rojoloco04/draftsmith/internal/opgg"
)

// testOPGG answers from the real OP.GG responses in opgg/testdata. The
// tier list is the capture with role_rate unless old is set.
type testOPGG struct {
	t   *testing.T
	old bool
}

func (f testOPGG) CallTool(_ context.Context, name string, _ map[string]any) (opgg.CallResult, error) {
	file := map[string]string{
		"lol_list_lane_meta_champions": "tierlist_roles.txt",
		"lol_list_champions":           "champions.txt",
		"lol_get_champion_analysis":    "analysis_ahri_mid.txt",
	}[name]
	if f.old && name == "lol_list_lane_meta_champions" {
		file = "tierlist_all.txt"
	}
	b, err := os.ReadFile("../opgg/testdata/" + file)
	if err != nil {
		f.t.Fatal(err)
	}
	return opgg.CallResult{Text: string(b)}, nil
}

type memCache map[string]db.MetaEntry

func (m memCache) MetaEntry(_ context.Context, key string) (db.MetaEntry, error) {
	if e, ok := m[key]; ok {
		return e, nil
	}
	return db.MetaEntry{}, db.ErrNotFound
}

func (m memCache) PutMetaEntry(_ context.Context, e db.MetaEntry) error {
	m[e.Key] = e
	return nil
}

func testService(t *testing.T, old bool) *meta.Service {
	return meta.NewWith(memCache{}, func() meta.Caller { return testOPGG{t, old} }, func() time.Time { return time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC) })
}

func tierList(t *testing.T) meta.TierList { return loadTierList(t, false) }

func loadTierList(t *testing.T, old bool) meta.TierList {
	t.Helper()
	tl, err := testService(t, old).TierList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return tl
}

// checkStochastic checks every slot's odds sum to 1 and no lane is
// over-assigned.
func checkStochastic(t *testing.T, o RoleOdds) {
	t.Helper()
	cols := make([]float64, len(Positions))
	for i, row := range o.P {
		sum := 0.0
		for j, p := range row {
			sum += p
			cols[j] += p
		}
		if math.Abs(sum-1) > 1e-9 {
			t.Errorf("slot %d (%s) sums to %v", i, o.Champions[i], sum)
		}
	}
	for j, c := range cols {
		if c > 1+1e-9 {
			t.Errorf("%s is assigned %v times", Positions[j], c)
		}
	}
}

func TestRolesRealEnemyTeam(t *testing.T) {
	// The enemy team from the captured ranked champ select.
	o := Roles(tierList(t), []string{"Seraphine", "Xayah", "Katarina", "Rengar", "Renekton"}, nil)
	checkStochastic(t, o)
	for i, want := range []string{"support", "adc", "mid", "jungle", "top"} {
		got, p := o.Likely(i)
		if got != want || p < 0.95 {
			t.Errorf("%s: %s %.3f, want %s", o.Champions[i], got, p, want)
		}
	}
}

func TestRolesFlexPickSettledByTeam(t *testing.T) {
	tl := tierList(t)
	// Swain is listed top, mid, bot and support. Alone, support is just
	// the most likely; next to a locked Xayah, bot is mostly ruled out.
	alone := Roles(tl, []string{"Swain", "", "", "", ""}, nil)
	withXayah := Roles(tl, []string{"Swain", "Xayah", "", "", ""}, nil)
	checkStochastic(t, alone)
	checkStochastic(t, withXayah)
	adc, sup := laneIndex("adc"), laneIndex("support")
	if l, _ := alone.Likely(0); l != "support" || alone.P[0][adc] < 0.1 {
		t.Errorf("Swain alone: %v", alone.P[0])
	}
	if withXayah.P[0][adc] > alone.P[0][adc]/3 || withXayah.P[0][sup] <= alone.P[0][sup] {
		t.Errorf("Xayah should push Swain off bot: alone %v, with Xayah %v", alone.P[0], withXayah.P[0])
	}
	// Open slots take whatever is left.
	if p := withXayah.P[2][adc]; p > 0.2 {
		t.Errorf("an open slot is bot with %v next to Xayah", p)
	}
}

func TestRolesOffRoleAndFixedLanes(t *testing.T) {
	tl := tierList(t)
	// Two junglers: one of them is off role, and the odds say so instead
	// of breaking.
	o := Roles(tl, []string{"Rengar", "Lee Sin", "", "", ""}, nil)
	checkStochastic(t, o)
	jg := laneIndex("jungle")
	if s := o.P[0][jg] + o.P[1][jg]; s < 0.95 {
		t.Errorf("someone should still be jungle: %v", s)
	}
	// A fixed lane wins over the champion's usual one (my team's slots).
	f := Roles(tl, []string{"Katarina", ""}, []string{"top", ""})
	if f.P[0][laneIndex("top")] != 1 || f.P[1][laneIndex("top")] != 0 {
		t.Errorf("fixed lane ignored: %v", f.P)
	}
	// An unknown champion is spread evenly.
	u := Roles(tl, []string{"NotAChampion"}, nil)
	if math.Abs(u.P[0][0]-0.2) > 1e-9 {
		t.Errorf("unknown champion: %v", u.P[0])
	}
}

func TestLaneShare(t *testing.T) {
	tl := tierList(t)
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
	// Swain is listed in four lanes at 13/14/20/54%; jungle, unlisted,
	// gets the floor.
	sw := laneShare(tl, "Swain")
	if !near(sw[laneIndex("support")], 0.54) || !near(sw[laneIndex("jungle")], offRole) {
		t.Errorf("Swain %v", sw)
	}
	// Seraphine is listed only as support (92%); the other 8% is spread
	// over the four lanes the list leaves her out of.
	se := laneShare(tl, "Seraphine")
	if !near(se[laneIndex("support")], 0.92) || !near(se[laneIndex("mid")], 0.02) {
		t.Errorf("Seraphine %v", se)
	}
	// A tier list cached before role_rate falls back to play counts.
	old := laneShare(loadTierList(t, true), "Swain")
	if !near(old[laneIndex("support")], 72559.0/(16963+19374+26529+72559)) {
		t.Errorf("Swain from play counts %v", old)
	}
}
