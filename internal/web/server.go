// Package web serves the dashboard: server-rendered html/template pages
// with htmx for filters and paging, everything embedded in the binary.
package web

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rojoloco04/draftsmith/internal/db"
	"github.com/rojoloco04/draftsmith/internal/riot"
)

var (
	//go:embed templates
	templateFS embed.FS
	//go:embed static
	staticFS embed.FS
)

// Status is what the footer shows about background sync.
type Status struct {
	Syncing   bool
	LastSync  time.Time // last finished sync, successful or not
	LastError string
	// SyncDisabled is set when serve runs without a Riot key or --no-sync.
	SyncDisabled string
}

// syncView is the header's refresh control.
type syncView struct {
	Status
	// CanRefresh is false when no refresh function is set.
	CanRefresh bool
	// Wait makes the control reload the page once the sync it started
	// finishes, so new games show up.
	Wait bool
}

// Account is one of my Riot accounts.
type Account struct {
	PUUID  string
	RiotID string
}

type Server struct {
	db       *db.DB
	accounts []Account
	log      *log.Logger
	pages    map[string]*template.Template
	meta     MetaSource

	mu      sync.Mutex
	status  Status
	refresh func() bool
	cs      champSelect
	// importRunes, if set, puts a rune page in the League client.
	importRunes RuneImporter
}

// New builds a server over an indexed database of my accounts' games.
// With more than one account, the header can switch between them and
// all of them together.
func New(d *db.DB, accounts []Account, logger *log.Logger) (*Server, error) {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	if len(accounts) == 0 {
		return nil, errors.New("web: no accounts")
	}
	s := &Server{db: d, accounts: accounts, log: logger, pages: map[string]*template.Template{}}
	for _, p := range []string{"overview", "matches", "match", "champions", "champion", "tierlist", "champselect", "error"} {
		t, err := template.New(p).Funcs(funcs).ParseFS(templateFS, "templates/layout.html", "templates/parts.html", "templates/prototype_switch.html", "templates/"+p+".html")
		if err != nil {
			return nil, fmt.Errorf("template %s: %w", p, err)
		}
		s.pages[p] = t
	}
	// The overlay is the champ select board without the dashboard around it.
	t, err := template.New("overlay").Funcs(funcs).ParseFS(templateFS, "templates/layout.html", "templates/parts.html", "templates/champselect.html", "templates/overlay.html")
	if err != nil {
		return nil, fmt.Errorf("template overlay: %w", err)
	}
	s.pages["overlay"] = t
	// PROTOTYPE: overview redesign variants.
	t, err = template.New("proto_overview").Funcs(funcs).ParseFS(templateFS, "templates/layout.html", "templates/parts.html", "templates/prototype_switch.html", "templates/prototype_overview.html")
	if err != nil {
		return nil, fmt.Errorf("template proto_overview: %w", err)
	}
	s.pages["proto_overview"] = t
	return s, nil
}

// SetStatus updates the sync status shown in the footer.
func (s *Server) SetStatus(f func(*Status)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(&s.status)
}

// SetRefresh enables the header's Refresh button. f asks for a sync now
// and reports whether it was queued. It's called with the server's lock
// held, so it must not block or call back into the server.
func (s *Server) SetRefresh(f func() bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh = f
}

func (s *Server) syncView(wait bool) syncView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return syncView{Status: s.status, CanRefresh: s.refresh != nil && s.status.SyncDisabled == "", Wait: wait}
}

func (s *Server) getStatus() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// Handler returns the dashboard's routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheFor(time.Hour, http.FileServerFS(static))))
	mux.HandleFunc("GET /{$}", s.overview)
	mux.HandleFunc("GET /matches", s.matches)
	mux.HandleFunc("GET /matches/{id}", s.match)
	mux.HandleFunc("GET /champions", s.champions)
	mux.HandleFunc("GET /champions/{name}", s.champion)
	mux.HandleFunc("GET /tier-list", s.tierListPage)
	mux.HandleFunc("GET /sync", s.syncStatus)
	mux.HandleFunc("POST /sync", s.syncNow)
	mux.HandleFunc("GET /account", s.switchAccount)
	mux.HandleFunc("GET /champ-select", s.champSelectPage)
	mux.HandleFunc("GET /champ-select/events", s.champSelectEvents)
	mux.HandleFunc("GET /overlay", s.overlayPage)
	mux.HandleFunc("POST /champ-select/runes", s.importRunesHandler)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		s.fail(w, r, http.StatusNotFound, errors.New("page not found"))
	})
	return logRequests(s.log, securityHeaders(mux))
}

// Base is the data every page's layout needs.
type Base struct {
	Title string
	Nav   string
	Path  string
	// RiotID names the selected account, or "All accounts".
	RiotID string
	// Account is the selected account, nil for all of them. Accounts is
	// every account; the header offers a switch when there are several.
	Account  *Account
	Accounts []Account
	Queue    queueOpt
	Queues   []queueOpt
	Status   Status
	Sync     syncView
	Now      time.Time
	// Rank is the latest snapshot for the header (solo queue unless flex
	// is selected); nil before the first one.
	Rank *db.RankSnapshot
}

type queueOpt struct {
	Key   string // solo, flex, all
	Label string
	ID    int // 0 for all
}

var queueOpts = []queueOpt{
	{"solo", "Ranked Solo/Duo", riot.QueueSoloDuo},
	{"flex", "Ranked Flex", riot.QueueFlex},
	{"all", "All queues", 0},
}

func queueFrom(r *http.Request) queueOpt {
	k := r.URL.Query().Get("queue")
	for _, q := range queueOpts {
		if q.Key == k {
			return q
		}
	}
	return queueOpts[0]
}

func (s *Server) base(r *http.Request, title, nav string) Base {
	acct := s.account(r)
	b := Base{
		Title: title, Nav: nav, Path: r.URL.Path, RiotID: "All accounts", Account: acct, Accounts: s.accounts,
		Queue: queueFrom(r), Queues: queueOpts,
		Status: s.getStatus(), Sync: s.syncView(false), Now: time.Now(),
	}
	if acct != nil {
		b.RiotID = acct.RiotID
		rank, err := s.db.LatestRank(r.Context(), acct.PUUID, rankQueue(b.Queue))
		if err != nil {
			s.log.Printf("latest rank: %v", err)
		}
		b.Rank = rank
	}
	return b
}

// acctCookie holds the selected account's Riot ID; without it (or with
// one no longer tracked) pages show every account.
const acctCookie = "account"

// account is the account selected for r, nil for all of them. With one
// account there is nothing to choose.
func (s *Server) account(r *http.Request) *Account {
	if len(s.accounts) == 1 {
		return &s.accounts[0]
	}
	c, err := r.Cookie(acctCookie)
	if err != nil {
		return nil
	}
	for i, a := range s.accounts {
		if strings.EqualFold(a.RiotID, c.Value) {
			return &s.accounts[i]
		}
	}
	return nil
}

// puuid is the selected account's PUUID, "" for all of them (a
// db.GameFilter's Account).
func (s *Server) puuid(r *http.Request) string {
	if a := s.account(r); a != nil {
		return a.PUUID
	}
	return ""
}

// switchAccount selects an account (?id=Name#TAG, or all) and goes back
// to the page it came from.
func (s *Server) switchAccount(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	c := &http.Cookie{Name: acctCookie, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode}
	id := q.Get("id")
	for _, a := range s.accounts {
		if strings.EqualFold(a.RiotID, id) {
			c.Value, c.MaxAge = a.RiotID, 365*24*3600
		}
	}
	if c.Value == "" {
		c.MaxAge = -1 // all accounts
	}
	http.SetCookie(w, c)
	http.Redirect(w, r, localPath(q.Get("back")), http.StatusSeeOther)
}

// localPath returns p if it's a path on this site, else "/", so the
// account switch can't redirect elsewhere.
func localPath(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") {
		return "/"
	}
	return p
}

// render executes a page. With block set (htmx partial requests) only that
// template is rendered.
func (s *Server) render(w http.ResponseWriter, r *http.Request, page, block string, data any) {
	t := s.pages[page]
	name := "layout"
	if block != "" {
		name = block
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, name, data); err != nil {
		s.log.Printf("render %s/%s: %v", page, name, err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Vary", "HX-Request")
	w.Write(buf.Bytes())
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, code int, err error) {
	if code >= 500 {
		s.log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
	}
	b := s.base(r, http.StatusText(code), "")
	data := struct {
		Base
		Code    int
		Message string
	}{b, code, sentence(err.Error())}
	if code >= 500 {
		data.Message = "Something went wrong reading the index. The server log has details."
	}
	var buf bytes.Buffer
	if err := s.pages["error"].ExecuteTemplate(&buf, "layout", data); err != nil {
		http.Error(w, data.Message, code)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	w.Write(buf.Bytes())
}

// syncNow starts a sync from the Refresh button and returns the control,
// which then polls GET /sync until the sync is done. Only htmx requests
// are accepted: a cross-site form can't set the HX-Request header, so
// other sites can't trigger syncs through the browser.
func (s *Server) syncNow(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("HX-Request") != "true" {
		http.Error(w, "refresh from the dashboard", http.StatusForbidden)
		return
	}
	s.mu.Lock()
	ok := s.refresh != nil && s.status.SyncDisabled == ""
	if ok && !s.status.Syncing && s.refresh() {
		s.status.Syncing = true // the loop sets it too; this shows it at once
	}
	s.mu.Unlock()
	if !ok {
		http.Error(w, "background sync is off", http.StatusConflict)
		return
	}
	s.render(w, r, "overview", "sync", s.syncView(true))
}

// syncStatus is the control's poll while a sync runs. With wait set, the
// page reloads once the sync is done.
func (s *Server) syncStatus(w http.ResponseWriter, r *http.Request) {
	v := s.syncView(r.URL.Query().Get("wait") == "1")
	if v.Wait && !v.Syncing {
		w.Header().Set("HX-Refresh", "true")
	}
	s.render(w, r, "overview", "sync", v)
}

// isPartial reports whether htmx asked for a fragment rather than a page.
// Boosted requests and history restores want the whole page.
func isPartial(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-Boosted") != "true" &&
		r.Header.Get("HX-History-Restore-Request") != "true"
}

func cacheFor(d time.Duration, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", int(d.Seconds())))
		h.ServeHTTP(w, r)
	})
}

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "no-referrer")
		hd.Set("X-Frame-Options", "DENY")
		// Images come from Riot's Data Dragon CDN and, for ranked
		// emblems, CommunityDragon; everything else is ours.
		hd.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' https://ddragon.leagueoflegends.com https://raw.communitydragon.org data:; style-src 'self' 'unsafe-inline'; script-src 'self'")
		h.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.code = code
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the real writer (to flush
// event streams).
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func logRequests(l *log.Logger, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		h.ServeHTTP(rec, r)
		if !strings.HasPrefix(r.URL.Path, "/static/") {
			l.Printf("%s %s %d %s", r.Method, r.URL.RequestURI(), rec.code, time.Since(start).Round(time.Millisecond))
		}
	})
}

// sentence capitalizes an error message and ends it with a period for the
// error page ("no indexed match" -> "No indexed match.").
func sentence(msg string) string {
	if msg == "" {
		return msg
	}
	msg = strings.ToUpper(msg[:1]) + msg[1:]
	if !strings.HasSuffix(msg, ".") {
		msg += "."
	}
	return msg
}
