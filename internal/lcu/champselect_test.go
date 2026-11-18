package lcu

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// The testdata sessions were captured from a real ranked champ select with
// watch --capture, so they hold only the fields Session declares.
func loadSession(t *testing.T, name string) Session {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var s Session
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRealChampSelectDuringBans(t *testing.T) {
	s := loadSession(t, "champselect_bans.json")
	me, ok := s.Me()
	if !ok || me.AssignedPosition != "middle" || me.Team != 1 {
		t.Fatalf("Me() = %+v, %v", me, ok)
	}
	if s.Bans.MyTeamBans != nil && len(s.Bans.MyTeamBans) != 0 {
		t.Errorf("ranked should leave Bans empty, got %+v", s.Bans)
	}
	// Three of my team's bans are in; the enemy's are hidden until the
	// reveal, and an in-progress ban hover doesn't count.
	mine, theirs := s.BanList()
	if !reflect.DeepEqual(mine, []int{7, 11, 53}) || len(theirs) != 0 {
		t.Errorf("bans %v / %v", mine, theirs)
	}
	if s.Locked(me.CellID) {
		t.Error("nothing is locked in during bans")
	}
}

func TestRealChampSelectFinal(t *testing.T) {
	s := loadSession(t, "champselect_final.json")
	mine, theirs := s.BanList()
	if !reflect.DeepEqual(mine, []int{7, 11, 133, 119, 53}) || !reflect.DeepEqual(theirs, []int{53, 236, 112, 777, 200}) {
		t.Errorf("bans %v / %v", mine, theirs)
	}
	for _, m := range append(s.MyTeam, s.TheirTeam...) {
		if !s.Locked(m.CellID) || m.ChampionID == 0 {
			t.Errorf("cell %d: champion %d, locked %v", m.CellID, m.ChampionID, s.Locked(m.CellID))
		}
	}
	for _, m := range s.TheirTeam {
		if m.AssignedPosition != "" {
			t.Errorf("enemy positions aren't shown in ranked, got %q", m.AssignedPosition)
		}
	}
}

func TestBanListHidesEnemyBansBeforeReveal(t *testing.T) {
	s := Session{Actions: [][]Action{
		{{Type: "ban", ChampionID: 1, Completed: true, IsAllyAction: true}, {Type: "ban", ChampionID: 2, Completed: true}},
		{{Type: "ten_bans_reveal", ActorCellID: -1}},
	}}
	if mine, theirs := s.BanList(); !reflect.DeepEqual(mine, []int{1}) || theirs != nil {
		t.Errorf("before reveal: %v / %v", mine, theirs)
	}
	s.Actions[1][0].Completed = true
	if _, theirs := s.BanList(); !reflect.DeepEqual(theirs, []int{2}) {
		t.Errorf("after reveal: %v", theirs)
	}
	// Modes that fill Bans themselves are taken as is.
	s.Bans = Bans{MyTeamBans: []int{9}, TheirTeamBans: []int{8}}
	if mine, theirs := s.BanList(); !reflect.DeepEqual(mine, []int{9}) || !reflect.DeepEqual(theirs, []int{8}) {
		t.Errorf("filled Bans: %v / %v", mine, theirs)
	}
}
