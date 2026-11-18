package watch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/rojoloco04/draftsmith/internal/lcu"
)

// fakeClient answers phases from a script; past the end it fails.
type fakeClient struct {
	phases   []string
	sessions []lcu.Session
}

func (f *fakeClient) ChampSelectSession(context.Context) (lcu.Session, error) {
	if len(f.sessions) == 0 {
		return lcu.Session{}, lcu.ErrNotFound
	}
	s := f.sessions[0]
	f.sessions = f.sessions[1:]
	return s, nil
}

func (f *fakeClient) GameflowPhase(context.Context) (string, error) {
	if len(f.phases) == 0 {
		return "", errors.New("connection refused")
	}
	p := f.phases[0]
	f.phases = f.phases[1:]
	return p, nil
}

func TestWatcherFollowsTheClient(t *testing.T) {
	a := lcu.Credentials{Port: 1000, Password: "pw-a", Source: "lockfile"}
	b := lcu.Credentials{Port: 2000, Password: "pw-b", Source: "lockfile"}
	// The client isn't running, then starts (a), closes leaving a stale
	// lockfile (a again, failing), and restarts with new credentials (b).
	found := []any{lcu.ErrNotRunning, lcu.ErrNotRunning, a, a, a, b}
	clients := map[int][]string{
		1000: {"None", "Lobby", "Lobby", "ChampSelect", "ChampSelect"},
		2000: {"Lobby"},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var out bytes.Buffer
	var phases []string
	w := &Watcher{
		Discover: func(context.Context) (lcu.Credentials, error) {
			if len(found) == 0 {
				cancel()
				return lcu.Credentials{}, ctx.Err()
			}
			f := found[0]
			found = found[1:]
			if err, ok := f.(error); ok {
				return lcu.Credentials{}, err
			}
			return f.(lcu.Credentials), nil
		},
		Connect: func(c lcu.Credentials) Client {
			f := &fakeClient{phases: clients[c.Port]}
			clients[c.Port] = nil // a second connection to a dead client fails
			return f
		},
		Log:       log.New(&out, "", 0),
		OnPhase:   func(p string) { phases = append(phases, p) },
		Poll:      time.Millisecond,
		ChampPoll: time.Millisecond,
		Retry:     time.Millisecond,
	}
	// b's single Lobby answer is followed by a failure, which leads to one
	// more Discover that ends the run.
	done := make(chan error)
	go func() { done <- w.Run(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watcher didn't stop")
	}

	if got := strings.Join(phases, ","); got != "None,Lobby,ChampSelect,,Lobby," {
		t.Errorf("phases %q", got)
	}
	logs := out.String()
	for _, want := range []string{
		"waiting for the League client\nfound the League client (port 1000",
		"lost the League client: connection refused\nphase: (no client)\nfound the League client (port 2000",
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs missing %q:\n%s", want, logs)
		}
	}
	// Waiting is logged once, and retrying the stale lockfile is silent.
	if n := strings.Count(logs, "waiting for"); n != 1 {
		t.Errorf("waiting logged %d times:\n%s", n, logs)
	}
	if n := strings.Count(logs, "port 1000"); n != 1 {
		t.Errorf("stale credentials logged %d times:\n%s", n, logs)
	}
	if strings.Contains(logs, "pw-") {
		t.Errorf("logs contain a password:\n%s", logs)
	}
}

func TestWatcherChampSelect(t *testing.T) {
	// A champ select as the client sends it, identity fields included;
	// decoding must drop them.
	raw := `{"localPlayerCellId":2,"isCustomGame":false,
	  "myTeam":[{"cellId":0,"team":1,"assignedPosition":"top","championId":0,"championPickIntent":0,"gameName":"Ally One","tagLine":"NA1","puuid":"ally-puuid","summonerId":111},
	            {"cellId":2,"team":1,"assignedPosition":"middle","championId":0,"championPickIntent":103,"gameName":"Me","puuid":"my-puuid"}],
	  "theirTeam":[{"cellId":5,"team":2,"assignedPosition":"","championId":0,"gameName":"","puuid":"enemy-puuid"}],
	  "actions":[[{"id":1,"actorCellId":2,"championId":0,"type":"ban","completed":false,"isInProgress":true,"isAllyAction":true}]],
	  "bans":{"myTeamBans":[],"theirTeamBans":[],"numBans":10},
	  "timer":{"phase":"PLANNING","adjustedTimeLeftInPhase":29000,"totalTimeInPhase":30000,"internalNowInEpochMs":1}}`
	var planning lcu.Session
	if err := json.Unmarshal([]byte(raw), &planning); err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(planning); strings.Contains(string(b), "puuid") || strings.Contains(string(b), "Ally One") {
		t.Fatalf("identity fields survived decoding: %s", b)
	}
	if me, ok := planning.Me(); !ok || me.AssignedPosition != "middle" {
		t.Errorf("Me() = %+v, %v", me, ok)
	}

	tick := planning
	tick.Timer.AdjustedTimeLeftInPhase = 28000 // only the clock moved
	picked := planning
	picked.Timer.Phase = "BAN_PICK"
	picked.MyTeam = append([]lcu.Member(nil), planning.MyTeam...)
	picked.MyTeam[1].ChampionID = 103
	picked.TheirTeam = []lcu.Member{{CellID: 5, Team: 2, ChampionID: 238}}
	picked.Bans.MyTeamBans = []int{55}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fc := &fakeClient{
		phases:   []string{"ChampSelect", "ChampSelect", "ChampSelect", "ChampSelect", "InProgress"},
		sessions: []lcu.Session{planning, tick, picked},
	}
	connected := false
	var out bytes.Buffer
	var got []lcu.Session
	w := &Watcher{
		Discover: func(context.Context) (lcu.Credentials, error) {
			if connected {
				cancel()
				return lcu.Credentials{}, ctx.Err()
			}
			connected = true
			return lcu.Credentials{Port: 1, Password: "pw"}, nil
		},
		Connect:   func(lcu.Credentials) Client { return fc },
		Log:       log.New(&out, "", 0),
		OnSession: func(s lcu.Session) { got = append(got, s) },
		Name:      func(id int) string { return map[int]string{103: "Ahri", 238: "Zed"}[id] },
		Poll:      time.Millisecond, ChampPoll: time.Millisecond, Retry: time.Millisecond,
	}
	if err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 updates (the timer tick isn't one), got %d:\n%s", len(got), out.String())
	}
	logs := out.String()
	for _, want := range []string{
		"champ select: PLANNING | us: top -, [middle Ahri?] | them: - | bans: - / -",
		"champ select: BAN_PICK | us: top -, [middle Ahri] | them: Zed | bans: #55 / -",
		"phase: InProgress",
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs missing %q:\n%s", want, logs)
		}
	}
}
