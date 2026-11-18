package watch

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/rojoloco04/draftsmith/internal/lcu"
)

// fakeClient answers phases from a script; past the end it fails.
type fakeClient struct {
	phases []string
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
		Log:     log.New(&out, "", 0),
		OnPhase: func(p string) { phases = append(phases, p) },
		Poll:    time.Millisecond,
		Retry:   time.Millisecond,
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
