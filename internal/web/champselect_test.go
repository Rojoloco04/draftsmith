package web

import (
	"bufio"
	"context"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rojoloco04/draftsmith/internal/draft"
	"github.com/rojoloco04/draftsmith/internal/lcu"
	"github.com/rojoloco04/draftsmith/internal/meta"
)

// testBoard is a mid lane draft: Galio suggested into a locked Katarina,
// Vladimir second with runes still loading.
func testBoard() draft.Board {
	galio := draft.Suggestion{Champion: "Galio", Games: 8, Wins: 6, MetaWinRate: 0.51, InMeta: true, WinProb: 0.58}
	galio.Meta.Tier = 2
	vlad := draft.Suggestion{Champion: "Vladimir", Games: 4, Wins: 1, MetaWinRate: math.NaN(), WinProb: 0.44}
	page := meta.RunePage{
		PrimaryStyle: 8200, Primary: []int{8229, 8226, 8210, 8237},
		SubStyle: 8000, Sub: []int{9111, 8014}, StatMods: []int{5008, 5008, 5001},
	}
	kat := draft.Champ{Key: "Katarina", Name: "Katarina"}
	return draft.Board{
		Phase: "BAN_PICK", Ends: time.Now().Add(25 * time.Second), Position: "mid", Patch: "16.22",
		MyTeam: []draft.Slot{
			{Champ: draft.Champ{Key: "Ornn", Name: "Ornn"}, Position: "top", Locked: true},
			{Champ: draft.Champ{Key: "LeeSin", Name: "Lee Sin"}, Position: "jungle"},
			{Position: "mid", Me: true, Autofill: true},
		},
		TheirTeam: []draft.Slot{
			{Champ: kat, Position: "mid", Odds: 0.97, Locked: true},
			{Position: "support", Odds: 0.6},
		},
		MyBans:    []draft.Champ{{Key: "Zed", Name: "Zed"}},
		TheirBans: []draft.Champ{{Key: "Leblanc", Name: "LeBlanc"}},
		Picks: []draft.Pick{
			{
				Champ: draft.Champ{Key: "Galio", Name: "Galio"}, Suggestion: galio, Scored: true, Reason: "Galio 58%: you're 6-2 on it",
				Vs:    []draft.Versus{{Champ: kat, Odds: 0.97, WinRate: 0.53, Games: 4213}},
				Runes: page, RunesVs: kat, RunesState: draft.RunesOK,
			},
			{
				Champ: draft.Champ{Key: "Vladimir", Name: "Vladimir"}, Suggestion: vlad, Scored: true,
				Vs: []draft.Versus{{Champ: kat, Odds: 0.97, WinRate: math.NaN()}}, RunesState: draft.RunesLoading,
			},
		},
	}
}

func TestChampSelectPage(t *testing.T) {
	srv, ts := newTestApp(t, nil)

	// Without a watcher the page says how to start one.
	_, body := get(t, ts, "/champ-select")
	expect(t, body, "serve --watch", "draftsmith watch")

	srv.WatchStarted("")
	srv.WatchPhase("Lobby")
	_, body = get(t, ts, "/champ-select")
	expect(t, body, "In a lobby", `data-stream="/champ-select/events"`)

	// A board that arrives outside champ select (a late rune fetch) is
	// dropped.
	srv.WatchBoard(testBoard())
	_, body = get(t, ts, "/champ-select")
	if strings.Contains(body, "Galio") {
		t.Error("board shown outside champ select")
	}

	srv.WatchPhase(lcu.PhaseChampSelect)
	srv.WatchBoard(testBoard())
	_, body = get(t, ts, "/champ-select")
	expect(t, body,
		"Bans and picks", `class="cs-timer" data-left="`,
		"Ornn", "Lee Sin", "Autofill", "Hover", // my team, with the hover marked
		"Katarina", "97%", "Picking…", // theirs, with lane odds
		"Banned: Zed", "Banned: LeBlanc",
		"Galio", "58%", "You're 6-2 on it", "51.0%", "53.0%</strong> into Katarina", "4.2k games",
		"Runes vs Katarina", "Arcane Comet",
		"Vladimir", "not on OP.GG's Mid list", "No matchup data vs Katarina", "Loading from OP.GG",
	)

	srv.WatchPhase("InProgress")
	_, body = get(t, ts, "/champ-select")
	expect(t, body, "In game")
	if strings.Contains(body, "Galio") {
		t.Error("board still shown after champ select")
	}
}

func TestOverlayPage(t *testing.T) {
	srv, ts := newTestApp(t, nil)
	srv.WatchStarted("")
	srv.WatchPhase(lcu.PhaseChampSelect)
	srv.WatchBoard(testBoard())

	code, body := get(t, ts, "/overlay")
	if code != http.StatusOK {
		t.Fatalf("GET /overlay: %d", code)
	}
	// The board and its stream, without the dashboard around it.
	expect(t, body, `<body class="overlay">`, `class="ov-hide" hidden`, `data-stream="/champ-select/events"`,
		"Galio", "Katarina", `<span class="pos-name">Mid</span>`)
	for _, s := range []string{`class="top"`, `class="nav"`, "<footer"} {
		if strings.Contains(body, s) {
			t.Errorf("overlay has %s", s)
		}
	}
}

func TestChampSelectLockedAndNote(t *testing.T) {
	srv, ts := newTestApp(t, nil)
	srv.WatchStarted("")
	srv.WatchPhase(lcu.PhaseChampSelect)

	b := testBoard()
	b.Picks, b.Note = nil, "None of your champions with 2+ games Mid is still available."
	srv.WatchBoard(b)
	_, body := get(t, ts, "/champ-select")
	expect(t, body, "None of your champions with 2&#43; games Mid")

	b = testBoard()
	b.Picks, b.Locked = b.Picks[:1], true
	b.Picks[0].RunesState = draft.RunesNone
	srv.WatchBoard(b)
	_, body = get(t, ts, "/champ-select")
	expect(t, body, "Locked in", "Galio", "OP.GG has no rune page")
	if strings.Contains(body, `class="cs-rank"`) {
		t.Error("a locked pick shouldn't be ranked")
	}

	srv.WatchStarted("no solo queue games indexed")
	srv.WatchPhase("Lobby")
	_, body = get(t, ts, "/champ-select")
	expect(t, body, "Suggestions are off: no solo queue games indexed")
}

// readEvent reads one server-sent event and returns its name and data.
func readEvent(t *testing.T, r *bufio.Reader) (string, string) {
	t.Helper()
	var name string
	var data []string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("reading the stream: %v", err)
		}
		line = strings.TrimRight(line, "\n")
		switch {
		case line == "" && name != "":
			return name, strings.Join(data, "\n")
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = append(data, strings.TrimPrefix(line, "data: "))
		}
	}
}

func TestChampSelectEvents(t *testing.T) {
	srv, ts := newTestApp(t, nil)
	srv.WatchStarted("")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/champ-select/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type %q", ct)
	}
	r := bufio.NewReader(resp.Body)

	// The current state comes at once.
	name, data := readEvent(t, r)
	if name != "board" || !strings.Contains(data, "Waiting for the League client") {
		t.Fatalf("first event %q:\n%s", name, data)
	}

	// Then every change.
	srv.WatchPhase(lcu.PhaseChampSelect)
	_, data = readEvent(t, r)
	expect(t, data, "Champ select")
	srv.WatchBoard(testBoard())
	_, data = readEvent(t, r)
	expect(t, data, "Galio", "Katarina", "Arcane Comet")
	if strings.Contains(data, "<html") {
		t.Error("an event should carry the fragment, not the page")
	}

	srv.WatchPhase("Lobby")
	_, data = readEvent(t, r)
	expect(t, data, "In a lobby")
}
