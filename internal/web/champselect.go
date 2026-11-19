package web

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/rojoloco04/draftsmith/internal/draft"
	"github.com/rojoloco04/draftsmith/internal/lcu"
)

// champSelect is what the champ select page shows. serve's watcher feeds
// it through the Server's Watch* methods; open pages follow it over
// server-sent events. Guarded by Server.mu.
type champSelect struct {
	on    bool   // a watcher feeds this server
	note  string // why there are no suggestions, if there can't be any
	phase string // the client's gameflow phase, "" without a client
	board draft.Board
	have  bool // board is the current champ select
	// changed is closed and replaced on every change, waking every stream.
	changed chan struct{}
}

// WatchStarted turns the champ select page on. note, if set, says why
// there will be no suggestions (the page still follows the client).
func (s *Server) WatchStarted(note string) {
	s.csUpdate(func(c *champSelect) bool {
		c.on, c.note = true, note
		return true
	})
}

// WatchPhase records the client's gameflow phase ("" when there's no
// client). Leaving champ select clears the board.
func (s *Server) WatchPhase(phase string) {
	s.csUpdate(func(c *champSelect) bool {
		if c.phase == phase {
			return false
		}
		c.phase = phase
		if phase != lcu.PhaseChampSelect {
			c.board, c.have = draft.Board{}, false
		}
		return true
	})
}

// WatchBoard shows a champ select. It's ignored outside one: a rune page
// fetched for the last draft can arrive after the client has moved on.
func (s *Server) WatchBoard(b draft.Board) {
	s.csUpdate(func(c *champSelect) bool {
		if c.phase != lcu.PhaseChampSelect {
			return false
		}
		c.board, c.have = b, true
		return true
	})
}

// csUpdate applies f and, if it reports a change, wakes every stream.
func (s *Server) csUpdate(f func(*champSelect) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !f(&s.cs) {
		return
	}
	if s.cs.changed != nil {
		close(s.cs.changed)
	}
	s.cs.changed = make(chan struct{})
}

// csView is the champ select as the "board" template draws it.
type csView struct {
	On    bool
	Note  string
	Phase string
	Have  bool
	Board draft.Board
	// Left is the countdown at render time, in ms (0 for none). The page
	// counts it down itself between updates.
	Left int64
}

// csSnapshot returns the current view and a channel closed on the next
// change.
func (s *Server) csSnapshot() (csView, <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cs.changed == nil {
		s.cs.changed = make(chan struct{})
	}
	c := s.cs
	v := csView{On: c.on, Note: c.note, Phase: c.phase, Have: c.have, Board: c.board}
	if !c.board.Ends.IsZero() {
		v.Left = max(0, time.Until(c.board.Ends).Milliseconds())
	}
	return v, c.changed
}

func (s *Server) champSelectPage(w http.ResponseWriter, r *http.Request) {
	v, _ := s.csSnapshot()
	s.render(w, r, "champselect", "", struct {
		Base
		CS csView
	}{s.base(r, "Champ select", "champ-select"), v})
}

// csKeepAlive is how often an idle event stream gets a comment, so
// proxies and the browser don't give up on it.
const csKeepAlive = 20 * time.Second

// champSelectEvents streams the board as server-sent "board" events, each
// carrying the rendered fragment: once on connect, then on every change.
// It ends when the client disconnects or the server shuts down.
func (s *Server) champSelectEvents(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	// The server's write timeout is for pages; a stream stays open.
	if err := rc.SetWriteDeadline(time.Time{}); err != nil && err != http.ErrNotSupported {
		s.log.Printf("champ select stream: %v", err)
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)

	keep := time.NewTicker(csKeepAlive)
	defer keep.Stop()
	var buf bytes.Buffer
	for {
		v, changed := s.csSnapshot()
		buf.Reset()
		if err := s.pages["champselect"].ExecuteTemplate(&buf, "board", v); err != nil {
			s.log.Printf("render champselect/board: %v", err)
			return
		}
		if err := writeEvent(w, "board", buf.String()); err != nil {
			return
		}
		if rc.Flush() != nil {
			return
		}
	wait:
		for {
			select {
			case <-r.Context().Done():
				return
			case <-changed:
				break wait
			case <-keep.C:
				if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil || rc.Flush() != nil {
					return
				}
			}
		}
	}
}

// writeEvent writes one server-sent event; each line of data becomes a
// data: field, which the browser joins back with newlines.
func writeEvent(w http.ResponseWriter, event, data string) error {
	var b strings.Builder
	b.WriteString("event: " + event + "\n")
	for _, line := range strings.Split(strings.ReplaceAll(data, "\r\n", "\n"), "\n") {
		b.WriteString("data: " + line + "\n")
	}
	b.WriteString("\n")
	_, err := fmt.Fprint(w, b.String())
	return err
}

// clientPhase describes the client's gameflow phase for the waiting card.
func clientPhase(p string) string {
	switch p {
	case "":
		return "Waiting for the League client"
	case "None":
		return "Client open"
	case "Lobby":
		return "In a lobby"
	case "Matchmaking":
		return "In queue"
	case "ReadyCheck":
		return "Ready check"
	case lcu.PhaseChampSelect:
		return "Champ select"
	case "GameStart", "InProgress", "Reconnect":
		return "In game"
	case "WaitingForStats", "PreEndOfGame", "EndOfGame":
		return "Post game"
	}
	return p
}

// champSelectPhase names a champ select timer phase.
func champSelectPhase(p string) string {
	switch p {
	case "PLANNING":
		return "Declaring picks"
	case "BAN_PICK":
		return "Bans and picks"
	case "FINALIZATION":
		return "Finalizing"
	case "GAME_STARTING":
		return "Game starting"
	}
	return "Champ select"
}
