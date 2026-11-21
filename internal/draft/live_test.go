package draft

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rojoloco04/draftsmith/internal/analysis"
	"github.com/rojoloco04/draftsmith/internal/lcu"
	"github.com/rojoloco04/draftsmith/internal/meta"
)

// syncBuffer is a log sink safe to read while Live writes from its fetcher.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// waitFor polls the log until it contains want.
func waitFor(t *testing.T, out *syncBuffer, want string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s := out.String(); strings.Contains(s, want) {
			return s
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("log never showed %q:\n%s", want, out.String())
	return ""
}

func loadCaptured(t *testing.T, name string) lcu.Session {
	t.Helper()
	b, err := os.ReadFile("../lcu/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var s lcu.Session
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestLiveRealChampSelect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src := testService(t, false)
	champs, err := src.Champions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byID, names := map[int]string{}, map[string]string{}
	for _, c := range champs.List {
		byID[c.ID] = c.Key
		names[c.Key] = c.Name
	}

	var mine []analysis.Game
	mine = append(mine, games("Galio", "MIDDLE", 6, 2)...)
	mine = append(mine, games("Vladimir", "MIDDLE", 3, 1)...)
	mine = append(mine, games("Yasuo", "MIDDLE", 3, 0)...)   // a teammate locked it
	mine = append(mine, games("Leblanc", "MIDDLE", 2, 2)...) // banned
	out := &syncBuffer{}
	var (
		bmu    sync.Mutex
		boards []Board
	)
	lastBoard := func() Board {
		bmu.Lock()
		defer bmu.Unlock()
		return boards[len(boards)-1]
	}
	l := &Live{
		Meta: src, Games: mine, Base: 0.5, MinGames: 2,
		Key: func(id int) string { return byID[id] }, Name: func(k string) string { return names[k] },
		Log: log.New(out, "", 0),
		OnBoard: func(b Board) {
			bmu.Lock()
			boards = append(boards, b)
			bmu.Unlock()
		},
	}
	l.Start(ctx)

	// The captured draft with everyone locked but me.
	final := loadCaptured(t, "champselect_final.json")
	open := final
	open.Actions = nil
	for _, turn := range final.Actions {
		var keep []lcu.Action
		for _, a := range turn {
			if a.ActorCellID == final.LocalPlayerCellID && a.Type == "pick" {
				a.Completed = false
			}
			keep = append(keep, a)
		}
		open.Actions = append(open.Actions, keep)
	}
	l.Update(open)

	// The first answer comes at once from what's cached; matchups and
	// runes follow from the background fetch.
	first := waitFor(t, out, "suggestions (Mid):")
	if !strings.Contains(first, "(loading from OP.GG)") {
		t.Errorf("first answer should not wait for OP.GG:\n%s", first)
	}
	waitFor(t, out, "runes vs Katarina: Sorcery: Stormraider's Surge")
	waitFor(t, out, "Adaptive Force, Adaptive Force, Health; spells Flash + Teleport")
	logs := waitFor(t, out, "runes (usual page)")
	last := logs[strings.LastIndex(logs, "suggestions (Mid):"):]
	for _, want := range []string{
		"1. Galio ", "into Katarina (mid 100%)",
		"2. Vladimir ", "(usual page)",
	} {
		if !strings.Contains(last, want) {
			t.Errorf("suggestions missing %q:\n%s", want, last)
		}
	}
	for _, line := range strings.Split(last, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "3.") || strings.Contains(line, "Yasuo") || strings.Contains(line, "LeBlanc") {
			t.Errorf("taken champion suggested or fetched: %s\n%s", line, logs)
		}
	}

	// The dashboard's board says the same, with the rest of the draft.
	b := lastBoard()
	if b.Position != "mid" || b.Locked || len(b.Picks) != 2 {
		t.Fatalf("board: position %q, locked %v, %d picks", b.Position, b.Locked, len(b.Picks))
	}
	if p := b.Picks[0]; p.Name != "Galio" || !p.Scored || p.RunesState != RunesOK || p.RunesVs.Name != "Katarina" ||
		len(p.Vs) == 0 || p.Vs[0].Name != "Katarina" || p.Vs[0].Odds < 0.99 || meta.SpellNames(p.Spells.IDs) != "Flash + Teleport" {
		t.Errorf("first pick: %+v", p)
	}
	if p := b.Picks[1]; p.Name != "Vladimir" || p.RunesState != RunesOK || p.RunesVs.Key != "" {
		t.Errorf("second pick: %+v", p)
	}
	if len(b.MyTeam) != 5 || len(b.TheirTeam) != 5 {
		t.Fatalf("teams: %d and %d", len(b.MyTeam), len(b.TheirTeam))
	}
	var lanes []string
	for _, sl := range b.MyTeam {
		lanes = append(lanes, sl.Position)
		if sl.Me && (sl.Position != "mid" || sl.Locked) {
			t.Errorf("my slot: %+v", sl)
		}
		if !sl.Me && (!sl.Locked || sl.Key == "") {
			t.Errorf("teammate should be locked: %+v", sl)
		}
	}
	if got := strings.Join(lanes, " "); got != "top jungle mid adc support" {
		t.Errorf("my team order: %s", got)
	}
	foundKat := false
	for _, sl := range b.TheirTeam {
		if sl.Name == "Katarina" {
			foundKat = sl.Position == "mid" && sl.Odds > 0.99 && sl.Locked
		}
	}
	if !foundKat {
		t.Errorf("their team: %+v", b.TheirTeam)
	}
	if len(b.MyBans)+len(b.TheirBans) == 0 || b.Phase == "" {
		t.Errorf("bans %v / %v, phase %q", b.MyBans, b.TheirBans, b.Phase)
	}

	// Locked in: just my champion's page for the matchup.
	l.Update(final)
	waitFor(t, out, "locked in Galio (Mid); runes vs Katarina: Sorcery: Stormraider's Surge")
	waitFor(t, out, "; build Doran's Ring + Health Potion x2 > ")
	if b := lastBoard(); !b.Locked || len(b.Picks) != 1 || b.Picks[0].Name != "Galio" || b.Picks[0].RunesState != RunesOK || len(b.Picks[0].Build.Core) == 0 {
		t.Errorf("locked board: %+v", b)
	}

	// Nothing new, nothing logged.
	n := len(out.String())
	bmu.Lock()
	nb := len(boards)
	bmu.Unlock()
	l.Update(final)
	time.Sleep(50 * time.Millisecond)
	bmu.Lock()
	if len(boards) != nb {
		t.Errorf("an unchanged update pushed %d more boards", len(boards)-nb)
	}
	bmu.Unlock()
	if len(out.String()) != n {
		t.Errorf("an unchanged update logged again:\n%s", out.String()[n:])
	}
}

func TestClientPosition(t *testing.T) {
	for in, want := range map[string]string{"top": "top", "jungle": "jungle", "middle": "mid", "bottom": "adc", "utility": "support", "": "", "UNSELECTED": ""} {
		if got := clientPosition(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// My hover gets its own card, first, with its rune page fetched ahead of
// the rest, whether or not it's a top suggestion.
func TestLiveShowsMyHover(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src := testService(t, false)
	champs, err := src.Champions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byID, names, ids := map[int]string{}, map[string]string{}, map[string]int{}
	for _, c := range champs.List {
		byID[c.ID] = c.Key
		names[c.Key] = c.Name
		ids[c.Key] = c.ID
	}
	var mine []analysis.Game
	mine = append(mine, games("Galio", "MIDDLE", 6, 2)...)
	mine = append(mine, games("Vladimir", "MIDDLE", 3, 1)...)
	var (
		bmu  sync.Mutex
		last Board
	)
	l := &Live{
		Meta: src, Games: mine, Base: 0.5, MinGames: 2,
		Key: func(id int) string { return byID[id] }, Name: func(k string) string { return names[k] },
		Log: log.New(&syncBuffer{}, "", 0),
		OnBoard: func(b Board) {
			bmu.Lock()
			last = b
			bmu.Unlock()
		},
	}
	l.Start(ctx)

	s := loadCaptured(t, "champselect_final.json")
	for i, turn := range s.Actions {
		for j, a := range turn {
			if a.ActorCellID == s.LocalPlayerCellID && a.Type == "pick" {
				s.Actions[i][j].Completed = false
			}
		}
	}
	for i, m := range s.MyTeam {
		if m.CellID == s.LocalPlayerCellID {
			s.MyTeam[i].ChampionID = ids["Vladimir"] // the second suggestion
		}
	}
	l.Update(s)
	deadline := time.Now().Add(5 * time.Second)
	for {
		bmu.Lock()
		b := last
		bmu.Unlock()
		if len(b.Picks) > 0 && b.Picks[0].RunesState == RunesOK {
			p := b.Picks[0]
			if !p.Mine || p.Name != "Vladimir" || !p.Scored {
				t.Fatalf("first card should be my hover: %+v", p)
			}
			for _, q := range b.Picks[1:] {
				if q.Name == "Vladimir" || q.Mine {
					t.Errorf("hover shown twice: %+v", b.Picks)
				}
			}
			if b.Comp.Known != 4 {
				t.Errorf("comp should count four teammates: %+v", b.Comp)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("my hover's runes never loaded: %+v", b.Picks)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
