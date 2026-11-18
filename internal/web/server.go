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

type Server struct {
	db     *db.DB
	riotID string
	log    *log.Logger
	pages  map[string]*template.Template
	meta   MetaSource

	mu     sync.Mutex
	status Status
}

// New builds a server over an indexed database. riotID is the tracked
// player, shown in the header.
func New(d *db.DB, riotID string, logger *log.Logger) (*Server, error) {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	s := &Server{db: d, riotID: riotID, log: logger, pages: map[string]*template.Template{}}
	for _, p := range []string{"overview", "matches", "match", "champions", "champion", "tierlist", "soon", "error"} {
		t, err := template.New(p).Funcs(funcs).ParseFS(templateFS, "templates/layout.html", "templates/parts.html", "templates/"+p+".html")
		if err != nil {
			return nil, fmt.Errorf("template %s: %w", p, err)
		}
		s.pages[p] = t
	}
	return s, nil
}

// SetStatus updates the sync status shown in the footer.
func (s *Server) SetStatus(f func(*Status)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(&s.status)
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
	mux.HandleFunc("GET /champ-select", s.soon("Champ select", "champ-select", "The live champ select helper arrives with `draftsmith watch` in Phase 3."))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		s.fail(w, r, http.StatusNotFound, errors.New("page not found"))
	})
	return logRequests(s.log, securityHeaders(mux))
}

// Base is the data every page's layout needs.
type Base struct {
	Title  string
	Nav    string
	Path   string
	RiotID string
	Queue  queueOpt
	Queues []queueOpt
	Status Status
	Now    time.Time
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
	return Base{
		Title: title, Nav: nav, Path: r.URL.Path, RiotID: s.riotID,
		Queue: queueFrom(r), Queues: queueOpts,
		Status: s.getStatus(), Now: time.Now(),
	}
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
	}{b, code, err.Error()}
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

func (s *Server) soon(title, nav, msg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.render(w, r, "soon", "", struct {
			Base
			Message string
		}{s.base(r, title, nav), msg})
	}
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
		// Images come from Riot's Data Dragon CDN; everything else is ours.
		hd.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' https://ddragon.leagueoflegends.com data:; style-src 'self' 'unsafe-inline'; script-src 'self'")
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
