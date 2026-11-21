package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/rojoloco04/draftsmith/internal/draft"
	"github.com/rojoloco04/draftsmith/internal/lcu"
	"github.com/rojoloco04/draftsmith/internal/liveclient"
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
	// game is my locked pick from the champ select that became the game
	// in progress, for its build. nil outside a game.
	game *gameView
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
		if c.phase == lcu.PhaseChampSelect && inGame(phase) && c.have && c.board.Locked && len(c.board.Picks) > 0 {
			c.game = &gameView{Pick: c.board.Picks[0], Position: c.board.Position, Patch: c.board.Patch}
		}
		if !inGame(phase) {
			c.game = nil
		}
		c.phase = phase
		if phase != lcu.PhaseChampSelect {
			c.board, c.have = draft.Board{}, false
		}
		return true
	})
}

// WatchBoard shows a champ select. Outside one, it only updates the game
// in progress with a late rune page or build for the champion I locked
// in; anything else for the last draft is dropped.
func (s *Server) WatchBoard(b draft.Board) {
	s.csUpdate(func(c *champSelect) bool {
		if inGame(c.phase) && c.game != nil && b.Locked && len(b.Picks) > 0 && b.Picks[0].Key == c.game.Pick.Key {
			c.game.Pick = b.Picks[0]
			return true
		}
		if c.phase != lcu.PhaseChampSelect {
			return false
		}
		c.board, c.have = b, true
		return true
	})
}

// WatchLive records my champion's state in the game in progress, or why
// it can't be read (liveclient.ErrNotReady while the game loads).
func (s *Server) WatchLive(snap liveclient.Snapshot, err error) {
	s.csUpdate(func(c *champSelect) bool {
		if c.game == nil {
			return false
		}
		if err != nil {
			c.game.Live, c.game.LiveErr = nil, err
		} else {
			c.game.Live, c.game.LiveErr = &snap, nil
		}
		return true
	})
}

// RuneImporter puts a rune page in the League client.
type RuneImporter func(ctx context.Context, page lcu.RunePage) error

// SetRuneImporter turns on the Import button on rune pages.
func (s *Server) SetRuneImporter(f RuneImporter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.importRunes = f
}

// importRunesHeader must be on import requests. A page on another site
// can't send a custom header here without a CORS preflight, which this
// server never answers, so only the dashboard can import.
const importRunesHeader = "X-Draftsmith"

// importRunesHandler puts the rune page shown for one champion (form value
// champ, its Riot key) in the client. It only runs when I press Import.
func (s *Server) importRunesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get(importRunesHeader) != "import" {
		http.Error(w, "import from the dashboard", http.StatusForbidden)
		return
	}
	key := r.FormValue("champ")
	s.mu.Lock()
	imp := s.importRunes
	var pick *draft.Pick
	if s.cs.game != nil && s.cs.game.Pick.Key == key {
		p := s.cs.game.Pick
		pick = &p
	}
	for _, p := range s.cs.board.Picks {
		if pick == nil && p.Key == key {
			p := p
			pick = &p
		}
	}
	s.mu.Unlock()
	switch {
	case imp == nil:
		http.Error(w, "Rune import needs draftsmith running on the PC League runs on, without --no-watch.", http.StatusConflict)
		return
	case pick == nil || pick.RunesState != draft.RunesOK:
		http.Error(w, "That rune page isn't on the board any more.", http.StatusNotFound)
		return
	}
	page := lcu.RunePage{PrimaryStyle: pick.Runes.PrimaryStyle, SubStyle: pick.Runes.SubStyle}
	page.Perks = append(append(append(page.Perks, pick.Runes.Primary...), pick.Runes.Sub...), pick.Runes.StatMods...)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := imp(ctx, page); err != nil {
		s.log.Printf("rune import for %s: %v", pick.Name, err)
		msg := "Couldn't import: " + err.Error()
		if errors.Is(err, lcu.ErrNoRoom) {
			msg = sentence(err.Error())
		}
		http.Error(w, msg, http.StatusBadGateway)
		return
	}
	s.log.Printf("imported %s's rune page into the client", pick.Name)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, "Imported")
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

// gameView is the game in progress: the pick I locked in, with its build,
// and my champion's state from the game when it can be read.
type gameView struct {
	Pick     draft.Pick
	Position string
	Patch    string
	Live     *liveclient.Snapshot
	LiveErr  error
	// Build is Pick's build laid out against Live, filled in by
	// csSnapshot.
	Build gameBuild
}

// inGame reports whether a gameflow phase is a game loading or running.
func inGame(phase string) bool {
	switch phase {
	case "GameStart", "InProgress", "Reconnect":
		return true
	}
	return false
}

// csView is the champ select as the "board" template draws it.
type csView struct {
	On    bool
	Note  string
	Phase string
	Have  bool
	Board draft.Board
	// Game is set during a game that started from a champ select seen here.
	Game *gameView
	// Left is the countdown at render time, in ms (0 for none). The page
	// counts it down itself between updates.
	Left int64
	// CanImport shows the Import button on rune pages.
	CanImport bool
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
	v := csView{On: c.on, Note: c.note, Phase: c.phase, Have: c.have, Board: c.board, CanImport: s.importRunes != nil}
	if c.game != nil {
		g := *c.game
		g.Build = newGameBuild(g.Pick.Build, g.Patch, g.Live)
		v.Game = &g
	}
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

// overlayPage is the board alone, for the overlay window.
func (s *Server) overlayPage(w http.ResponseWriter, r *http.Request) {
	v, _ := s.csSnapshot()
	w.Header().Set("Cache-Control", "no-store")
	s.render(w, r, "overlay", "overlay", struct {
		CS    csView
		Theme string
	}{v, s.Theme()})
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
