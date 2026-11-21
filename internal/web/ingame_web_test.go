package web

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rojoloco04/draftsmith/internal/draft"
	"github.com/rojoloco04/draftsmith/internal/lcu"
	"github.com/rojoloco04/draftsmith/internal/liveclient"
	"github.com/rojoloco04/draftsmith/internal/meta"
)

// In game, the build card follows my items and level, and a build that
// arrives after the game starts still shows.
func TestInGameTracking(t *testing.T) {
	srv, ts := newTestApp(t, nil)
	srv.WatchStarted("")
	srv.WatchPhase(lcu.PhaseChampSelect)
	b := testBoard()
	b.Picks, b.Locked = b.Picks[:1], true
	b.Picks[0].RunesState, b.Picks[0].Build = draft.RunesLoading, meta.Build{}
	srv.WatchBoard(b)
	srv.WatchPhase("InProgress")

	_, body := get(t, ts, "/champ-select")
	expect(t, body, "Loading the build from OP.GG")

	// The rune page and build land mid-game.
	b.Picks[0].RunesState, b.Picks[0].Build = draft.RunesOK, testBuild()
	srv.WatchBoard(b)
	// A board for some other champion is ignored.
	other := b
	other.Picks = []draft.Pick{{Champ: draft.Champ{Key: "Ahri", Name: "Ahri"}, RunesState: draft.RunesOK}}
	srv.WatchBoard(other)

	srv.WatchLive(liveclient.Snapshot{}, liveclient.ErrNotReady)
	_, body = get(t, ts, "/champ-select")
	expect(t, body, "Build vs Katarina", "Waiting for the game to load", `alt="Hextech Rocketbelt"`)
	if strings.Contains(body, "Ahri") {
		t.Error("another champion's board replaced the game's")
	}

	srv.WatchLive(liveclient.Snapshot{Level: 3, Skills: map[string]int{"Q": 1, "W": 1},
		Items: []liveclient.Item{{ID: 1056, Name: "Doran's Ring", Count: 1}, {ID: 2003, Name: "Health Potion", Count: 1}}}, nil)
	_, body = get(t, ts, "/champ-select")
	expect(t, body, "Level 3", "Your items", "Buy next", `title="Hextech Rocketbelt"`, "Level up", `<b class="key k-E">E</b>`,
		`class="k-E next"`)
}

func TestImportRunes(t *testing.T) {
	srv, ts := newTestApp(t, nil)
	srv.WatchStarted("")
	srv.WatchPhase(lcu.PhaseChampSelect)
	srv.WatchBoard(testBoard())

	// Without an importer there's no button, and no import.
	_, body := get(t, ts, "/champ-select")
	if strings.Contains(body, "data-import=") {
		t.Error("Import button without an importer")
	}
	if code, _, _ := post(t, ts, "/champ-select/runes?champ=Galio", "X-Draftsmith", "import"); code != 409 {
		t.Errorf("import without an importer: %d", code)
	}

	var got []lcu.RunePage
	fail := error(nil)
	srv.SetRuneImporter(func(ctx context.Context, p lcu.RunePage) error {
		got = append(got, p)
		return fail
	})
	_, body = get(t, ts, "/champ-select")
	expect(t, body, `data-import="Galio"`, "Import to client")

	// A plain form post (another site) is refused.
	if code, _, _ := post(t, ts, "/champ-select/runes?champ=Galio"); code != 403 || len(got) != 0 {
		t.Errorf("post without the header: %d, %d imports", code, len(got))
	}
	// A page that isn't loaded can't be imported.
	if code, _, _ := post(t, ts, "/champ-select/runes?champ=Vladimir", "X-Draftsmith", "import"); code != 404 {
		t.Errorf("loading page: %d", code)
	}
	code, msg, _ := post(t, ts, "/champ-select/runes?champ=Galio", "X-Draftsmith", "import")
	if code != 200 || msg != "Imported" || len(got) != 1 {
		t.Fatalf("import: %d %q, %d imports", code, msg, len(got))
	}
	if p := got[0]; p.PrimaryStyle != 8200 || p.SubStyle != 8000 ||
		len(p.Perks) != 9 || p.Perks[0] != 8229 || p.Perks[4] != 9111 || p.Perks[8] != 5001 {
		t.Errorf("page: %+v", p)
	}

	fail = lcu.ErrNoRoom
	if code, msg, _ := post(t, ts, "/champ-select/runes?champ=Galio", "X-Draftsmith", "import"); code != 502 || !strings.Contains(msg, "No free rune page") {
		t.Errorf("no room: %d %q", code, msg)
	}
	fail = errors.New("boom")
	if code, msg, _ := post(t, ts, "/champ-select/runes?champ=Galio", "X-Draftsmith", "import"); code != 502 || !strings.Contains(msg, "Couldn't import: boom") {
		t.Errorf("error: %d %q", code, msg)
	}
}

// Comp, my hover and its note show on the board.
func TestBoardCompAndHover(t *testing.T) {
	srv, ts := newTestApp(t, nil)
	srv.WatchStarted("")
	srv.WatchPhase(lcu.PhaseChampSelect)
	b := testBoard()
	b.Comp = draft.Comp([]string{"Ornn", "LeeSin"})
	b.Picks[0].Mine = true
	b.Picks[0].Comp, b.Picks[0].CompNote = 0.1, "adds AP to a mostly AD team"
	srv.WatchBoard(b)
	_, body := get(t, ts, "/champ-select")
	expect(t, body, "Your team", "mostly AD · 1 frontliner", "Your hover", "adds AP to a mostly AD team")
}
