package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadStateMigratesOneAccount(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	legacy := `{"puuid":"p-1","riotId":"Me#NA1","platform":"na1","backfillDone":{"420":true},"lastSync":"2026-11-18T20:00:00Z"}`
	if err := os.WriteFile(filepath.Join(s.Dir, "state.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "ranks", "2026-11-17.json"), []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := s.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Accounts) != 1 {
		t.Fatalf("accounts: %+v", st.Accounts)
	}
	a := st.Accounts[0]
	if a.PUUID != "p-1" || a.RiotID != "Me#NA1" || a.Platform != "na1" || !a.BackfillDone[420] || a.LastSync.IsZero() {
		t.Errorf("account: %+v", a)
	}
	if days, err := s.RankSnapshotDays("p-1"); err != nil || len(days) != 1 || days[0] != "2026-11-17" {
		t.Errorf("rank days: %v, %v", days, err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "ranks", "2026-11-17.json")); !os.IsNotExist(err) {
		t.Errorf("old snapshot still in ranks/: %v", err)
	}

	// The new format is saved, so the next load reads it as is.
	b, _ := os.ReadFile(filepath.Join(s.Dir, "state.json"))
	var saved map[string]any
	json.Unmarshal(b, &saved)
	if _, ok := saved["accounts"]; !ok || saved["puuid"] != nil {
		t.Errorf("saved state: %s", b)
	}
	again, err := s.LoadState()
	if err != nil || len(again.Accounts) != 1 || again.Accounts[0].PUUID != "p-1" {
		t.Errorf("reload: %+v, %v", again, err)
	}
}

func TestAccounts(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st, err := s.LoadState()
	if err != nil || len(st.Accounts) != 0 {
		t.Fatalf("empty dir: %+v, %v", st, err)
	}
	t1 := time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC)
	st.Accounts = []Account{
		{PUUID: "a", RiotID: "A#NA1", LastSync: t1},
		{PUUID: "b", RiotID: "B#SLU", LastSync: t1.Add(time.Hour)},
	}
	if err := s.SaveState(st); err != nil {
		t.Fatal(err)
	}
	st, err = s.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if got := st.RiotIDs(); got != "A#NA1, B#SLU" {
		t.Errorf("RiotIDs: %q", got)
	}
	if !st.LastSync().Equal(t1.Add(time.Hour)) {
		t.Errorf("LastSync: %v", st.LastSync())
	}
	if a := st.Find("b"); a == nil || a.RiotID != "B#SLU" || a.BackfillDone == nil {
		t.Errorf("Find: %+v", a)
	}
	if st.Find("c") != nil {
		t.Error("Find found a stranger")
	}

	// Rank snapshots are kept apart per account.
	if err := s.PutRankSnapshot("a", t1, []int{1}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutRankSnapshot("b", t1.AddDate(0, 0, 1), []int{2}); err != nil {
		t.Fatal(err)
	}
	if b, day, err := s.LatestRankSnapshot("b"); err != nil || day != "2026-11-19" || string(b) == "" {
		t.Errorf("latest for b: %q %q %v", b, day, err)
	}
	if days, _ := s.RankSnapshotDays("a"); len(days) != 1 || days[0] != "2026-11-18" {
		t.Errorf("days for a: %v", days)
	}
	if days, err := s.RankSnapshotDays("nobody"); err != nil || len(days) != 0 {
		t.Errorf("days for an unknown account: %v, %v", days, err)
	}
}

func TestUpdateState(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateState(func(st *State) error {
		st.Accounts = append(st.Accounts, Account{PUUID: "p1", RiotID: "A#1"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateState(func(st *State) error {
		st.Accounts = nil
		return errors.New("no")
	}); err == nil {
		t.Fatal("f's error should come back")
	}
	st, _ := s.LoadState()
	if len(st.Accounts) != 1 || st.Accounts[0].RiotID != "A#1" {
		t.Errorf("state after a failed update: %+v", st.Accounts)
	}
}
