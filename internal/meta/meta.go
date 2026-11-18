// Package meta wraps the OP.GG tools Draftsmith uses (tier list, champion
// list, champion analysis) in typed calls, cached in SQLite.
//
// Each response is cached as raw text and parsed on every read, so a parser
// fix also applies to cached data. Entries are refreshed once they're older
// than a day. If OP.GG is down or its answer no longer parses, the last good
// copy is served with the error attached, so pages can show its age instead
// of failing.
package meta

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rojoloco04/draftsmith/internal/db"
	"github.com/rojoloco04/draftsmith/internal/opgg"
)

// Bracket is the rank filter for champion analysis. The tier list has no
// filter and is always Emerald+, which is also the bracket I play in.
const Bracket = "emerald_plus"

// How long a cached response counts as fresh.
const (
	TierListMaxAge  = 24 * time.Hour
	AnalysisMaxAge  = 24 * time.Hour
	ChampionsMaxAge = 7 * 24 * time.Hour
)

// RetryAfter is how long to wait after a failed refresh before trying the
// same key again. Meanwhile the stale copy is served without a network call.
const RetryAfter = 5 * time.Minute

const clientApp = "Draftsmith"

// Positions are OP.GG's lanes in Riot's lane order.
var Positions = []string{"top", "jungle", "mid", "adc", "support"}

var positionByRole = map[string]string{"TOP": "top", "JUNGLE": "jungle", "MIDDLE": "mid", "BOTTOM": "adc", "UTILITY": "support"}

// Position maps a Riot teamPosition to OP.GG's position, or "".
func Position(role string) string { return positionByRole[role] }

// Role maps an OP.GG position back to Riot's teamPosition, or "".
func Role(position string) string {
	for r, p := range positionByRole {
		if p == position {
			return r
		}
	}
	return ""
}

// PositionName is a position for display.
func PositionName(p string) string {
	switch p {
	case "adc":
		return "Bot"
	case "mid":
		return "Mid"
	case "":
		return ""
	}
	return strings.ToUpper(p[:1]) + p[1:]
}

// Caller is the part of opgg.Client used here.
type Caller interface {
	CallTool(ctx context.Context, name string, args map[string]any) (opgg.CallResult, error)
}

// Cache stores raw responses; *db.DB implements it.
type Cache interface {
	MetaEntry(ctx context.Context, key string) (db.MetaEntry, error)
	PutMetaEntry(ctx context.Context, e db.MetaEntry) error
}

// Service fetches and caches OP.GG data. It is safe for concurrent use;
// network calls are made one at a time.
type Service struct {
	cache Cache
	dial  func() Caller
	now   func() time.Time
	// cacheOnly serves whatever is cached, however old, and never calls
	// OP.GG.
	cacheOnly bool

	mu     sync.Mutex
	failed map[string]time.Time // key -> last failed refresh
}

// New returns a service that talks to OP.GG at endpoint ("" for the
// default). Each refresh opens a new MCP session, so a session the server
// expired during a long serve never gets stuck.
func New(cache Cache, endpoint string) *Service {
	return NewWith(cache, func() Caller { return opgg.New(endpoint) }, time.Now)
}

// NewWith is New with the client and clock injected, for tests.
func NewWith(cache Cache, dial func() Caller, now func() time.Time) *Service {
	return &Service{cache: cache, dial: dial, now: now, failed: map[string]time.Time{}}
}

// NewCacheOnly returns a service that never calls OP.GG: it serves whatever
// is cached, however old, and returns ErrNotCached for anything else. The
// report uses it so that it never touches the network for meta data.
func NewCacheOnly(cache Cache) *Service {
	return &Service{cache: cache, now: time.Now, cacheOnly: true, failed: map[string]time.Time{}}
}

// ErrNotCached is returned by a cache-only service for data it doesn't have.
var ErrNotCached = errors.New("not in the OP.GG cache")

// Freshness says how old served data is.
type Freshness struct {
	FetchedAt time.Time
	// Patch is the game patch the data is for, when the response says.
	Patch string
	// RefreshErr is set when the data is an older cached copy because the
	// refresh failed.
	RefreshErr error
}

// Stale reports whether a refresh failed and older data is being served.
func (f Freshness) Stale() bool { return f.RefreshErr != nil }

// request is one cacheable tool call. decode parses a body into the typed
// result, replacing whatever it held, and returns the patch it is for (""
// if unknown); it must fail on anything unusable so that a bad answer is
// never cached.
type request struct {
	key    string
	tool   string
	args   map[string]any
	maxAge time.Duration
	decode func(body string) (patch string, err error)
}

// get serves req from the cache when fresh, refreshes it otherwise, and
// falls back to the cached copy if the refresh fails. On success decode has
// been run on the body that is returned, so its target holds the result.
func (s *Service) get(ctx context.Context, req request) (Freshness, error) {
	cached, have, err := s.cached(ctx, req)
	if err != nil {
		return Freshness{}, err
	}
	if have && (s.cacheOnly || s.now().Sub(cached.FetchedAt) < req.maxAge) {
		return Freshness{FetchedAt: cached.FetchedAt, Patch: cached.Patch}, nil
	}
	if s.cacheOnly {
		return Freshness{}, ErrNotCached
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// Another request may have refreshed it while this one waited.
	if cached, have, err = s.cached(ctx, req); err != nil {
		return Freshness{}, err
	}
	if have && s.now().Sub(cached.FetchedAt) < req.maxAge {
		return Freshness{FetchedAt: cached.FetchedAt, Patch: cached.Patch}, nil
	}
	if t, ok := s.failed[req.key]; ok && have && s.now().Sub(t) < RetryAfter {
		return Freshness{FetchedAt: cached.FetchedAt, Patch: cached.Patch, RefreshErr: errors.New("OP.GG refresh failed recently; retrying in a few minutes")}, nil
	}

	fresh, ferr := s.fetch(ctx, req)
	if ferr == nil {
		delete(s.failed, req.key)
		if err := s.cache.PutMetaEntry(ctx, fresh); err != nil {
			return Freshness{}, err
		}
		return Freshness{FetchedAt: fresh.FetchedAt, Patch: fresh.Patch}, nil
	}
	s.failed[req.key] = s.now()
	if !have {
		return Freshness{}, ferr
	}
	if _, err := req.decode(cached.Body); err != nil { // fetch may have left the target half filled
		return Freshness{}, ferr
	}
	return Freshness{FetchedAt: cached.FetchedAt, Patch: cached.Patch, RefreshErr: ferr}, nil
}

// cached reads and decodes the cache entry for req. An entry that no longer
// decodes counts as missing.
func (s *Service) cached(ctx context.Context, req request) (db.MetaEntry, bool, error) {
	e, err := s.cache.MetaEntry(ctx, req.key)
	if errors.Is(err, db.ErrNotFound) {
		return db.MetaEntry{}, false, nil
	}
	if err != nil {
		return db.MetaEntry{}, false, err
	}
	if _, err := req.decode(e.Body); err != nil {
		return db.MetaEntry{}, false, nil
	}
	return e, true, nil
}

func (s *Service) fetch(ctx context.Context, req request) (db.MetaEntry, error) {
	args := map[string]any{"client_app": clientApp}
	for k, v := range req.args {
		args[k] = v
	}
	res, err := s.dial().CallTool(ctx, req.tool, args)
	if err != nil {
		return db.MetaEntry{}, err
	}
	if res.IsError {
		return db.MetaEntry{}, fmt.Errorf("opgg %s: %s", req.tool, firstLine(res.Text))
	}
	patch, err := req.decode(res.Text)
	if err != nil {
		return db.MetaEntry{}, fmt.Errorf("opgg %s: %w", req.tool, err)
	}
	return db.MetaEntry{Key: req.key, FetchedAt: s.now(), Patch: patch, Body: res.Text}, nil
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

// normalize folds a champion name or key for loose matching: "Cho'Gath",
// "CHO_GATH" and "Chogath" all become "chogath".
func normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}
