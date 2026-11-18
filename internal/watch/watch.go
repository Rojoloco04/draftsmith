// Package watch follows the League client through its gameflow phases and,
// in later steps, champ select. It only reads; it never acts in the client.
package watch

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/rojoloco04/draftsmith/internal/lcu"
)

// Client is the part of *lcu.Client the watcher uses.
type Client interface {
	GameflowPhase(ctx context.Context) (string, error)
}

// Watcher polls the client's gameflow phase, reconnecting whenever the
// client restarts or closes.
type Watcher struct {
	Discover func(ctx context.Context) (lcu.Credentials, error)
	Connect  func(lcu.Credentials) Client
	Log      *log.Logger
	// OnPhase is called on every phase change, including "" when the client
	// goes away.
	OnPhase func(phase string)

	// Poll is how often the phase is checked; Retry how often to look for
	// a client that isn't running.
	Poll, Retry time.Duration
}

// Run watches until ctx is done.
func (w *Watcher) Run(ctx context.Context) error {
	poll, retry := w.Poll, w.Retry
	if poll <= 0 {
		poll = 2 * time.Second
	}
	if retry <= 0 {
		retry = 5 * time.Second
	}
	var (
		c       Client
		phase   string
		lastErr string // last discovery problem logged, to log each once
		// failed are the credentials that last stopped working. Finding
		// them again (a stale lockfile, or a client still starting up) is
		// retried without logging each attempt.
		failed lcu.Credentials
		quiet  bool
		cur    lcu.Credentials
	)
	setPhase := func(p string) {
		if p == phase {
			return
		}
		phase = p
		if p == "" {
			w.Log.Printf("phase: (no client)")
		} else {
			w.Log.Printf("phase: %s", p)
		}
		if w.OnPhase != nil {
			w.OnPhase(p)
		}
	}
	for {
		if c == nil {
			creds, err := w.Discover(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				msg := err.Error()
				if errors.Is(err, lcu.ErrNotRunning) {
					msg = "waiting for the League client"
				}
				if msg != lastErr {
					w.Log.Print(msg)
					lastErr = msg
				}
				if !sleep(ctx, retry) {
					return nil
				}
				continue
			}
			lastErr = ""
			quiet = creds.Port == failed.Port && creds.Password == failed.Password
			if !quiet {
				w.Log.Printf("found the League client (%s)", creds)
			}
			c = w.Connect(creds)
			cur = creds
		}

		p, err := c.GameflowPhase(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			// The client closed or restarted with new credentials (a stale
			// lockfile looks the same). Look for it again.
			if !quiet {
				w.Log.Printf("lost the League client: %v", err)
			}
			failed, c = cur, nil
			setPhase("")
			if !sleep(ctx, retry) {
				return nil
			}
			continue
		}
		if quiet {
			w.Log.Printf("connected to the League client (%s)", cur)
			quiet, failed = false, lcu.Credentials{}
		}
		setPhase(p)
		if !sleep(ctx, poll) {
			return nil
		}
	}
}

// sleep waits for d, returning false if ctx ends first.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
