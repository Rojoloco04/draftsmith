// Package watch follows the League client through its gameflow phases and,
// in later steps, champ select. It only reads; it never acts in the client.
package watch

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/rojoloco04/draftsmith/internal/lcu"
)

// Client is the part of *lcu.Client the watcher uses.
type Client interface {
	GameflowPhase(ctx context.Context) (string, error)
	ChampSelectSession(ctx context.Context) (lcu.Session, error)
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
	// OnSession is called with each champ select update that changes
	// anything but the timer.
	OnSession func(lcu.Session)
	// Name turns a champion id into a name for logs; nil logs ids.
	Name func(id int) string

	// Poll is how often the phase is checked outside champ select,
	// ChampPoll how often the session is read during it, and Retry how
	// often to look for a client that isn't running.
	Poll, ChampPoll, Retry time.Duration
}

// Run watches until ctx is done.
func (w *Watcher) Run(ctx context.Context) error {
	poll, champPoll, retry := w.Poll, w.ChampPoll, w.Retry
	if poll <= 0 {
		poll = 2 * time.Second
	}
	if champPoll <= 0 {
		champPoll = time.Second
	}
	if retry <= 0 {
		retry = 5 * time.Second
	}
	var (
		c       Client
		phase   string
		lastSel string // last champ select summary, to report changes only
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
		wait := poll
		if p == lcu.PhaseChampSelect {
			wait = champPoll
			sess, err := c.ChampSelectSession(ctx)
			switch {
			case err == nil:
				if sum := w.summarize(sess); sum != lastSel {
					lastSel = sum
					w.Log.Printf("champ select: %s", sum)
					if w.OnSession != nil {
						w.OnSession(sess)
					}
				}
			case errors.Is(err, lcu.ErrNotFound): // champ select just ended
			case ctx.Err() != nil:
				return nil
			default:
				w.Log.Printf("champ select session: %v", err)
			}
		} else {
			lastSel = ""
		}
		if !sleep(ctx, wait) {
			return nil
		}
	}
}

// summarize describes a champ select in one line: phase, my slot, both
// teams' champions and the bans. It leaves out the timer's countdown so
// that it only changes when something happens.
func (w *Watcher) summarize(s lcu.Session) string {
	name := func(id int) string {
		if id == 0 {
			return "-"
		}
		if w.Name != nil {
			if n := w.Name(id); n != "" {
				return n
			}
		}
		return fmt.Sprintf("#%d", id)
	}
	team := func(ms []lcu.Member) string {
		var parts []string
		for _, m := range ms {
			p := name(m.ChampionID)
			if m.ChampionID == 0 && m.ChampionPickIntent != 0 {
				p = name(m.ChampionPickIntent) + "?"
			}
			if m.AssignedPosition != "" {
				p = m.AssignedPosition + " " + p
			}
			if m.CellID == s.LocalPlayerCellID { // cell ids are unique across both teams
				p = "[" + p + "]"
			}
			parts = append(parts, p)
		}
		return strings.Join(parts, ", ")
	}
	ids := func(xs []int) string {
		var parts []string
		for _, id := range xs {
			parts = append(parts, name(id))
		}
		if len(parts) == 0 {
			return "-"
		}
		return strings.Join(parts, " ")
	}
	return fmt.Sprintf("%s | us: %s | them: %s | bans: %s / %s",
		s.Timer.Phase, team(s.MyTeam), team(s.TheirTeam), ids(s.Bans.MyTeamBans), ids(s.Bans.TheirTeamBans))
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
