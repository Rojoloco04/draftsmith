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
	l := &Live{
		Meta: src, Games: mine, Base: 0.5, MinGames: 2,
		Key: func(id int) string { return byID[id] }, Name: func(k string) string { return names[k] },
		Log: log.New(out, "", 0),
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

	// Locked in: just my champion's page for the matchup.
	l.Update(final)
	waitFor(t, out, "locked in Galio (Mid); runes vs Katarina: Sorcery: Stormraider's Surge")

	// Nothing new, nothing logged.
	n := len(out.String())
	l.Update(final)
	time.Sleep(50 * time.Millisecond)
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
