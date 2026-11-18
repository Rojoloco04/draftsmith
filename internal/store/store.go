// Package store archives raw Riot JSON on disk as gzipped files.
//
// Layout under the data dir:
//
//	state.json                  who we sync for and backfill progress
//	matches/<matchId>.json.gz   raw match-v5 responses
//	timelines/<matchId>.json.gz raw timeline responses
//	timelines/<matchId>.missing marker for timelines Riot no longer has
//	ranks/<date>.json           daily snapshot of league-v4 standings
//	meta/<key>.json             cached OP.GG responses
//
// Raw responses are kept untouched so new stats can be computed later
// without calling the API again, which matters because Riot deletes matches
// after about two years.
package store

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Store struct {
	Dir string
}

type State struct {
	PUUID    string `json:"puuid"`
	RiotID   string `json:"riotId"`
	Platform string `json:"platform"`
	// BackfillDone records, per queue, whether a full pass over the match id
	// list has completed. Until it has, sync pages all the way back instead
	// of stopping at the first page of already-archived games.
	BackfillDone map[int]bool `json:"backfillDone"`
	LastSync     time.Time    `json:"lastSync"`
}

func Open(dir string) (*Store, error) {
	for _, sub := range []string{"matches", "timelines", "ranks", "meta"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, err
		}
	}
	return &Store{Dir: dir}, nil
}

func (s *Store) LoadState() (State, error) {
	var st State
	b, err := os.ReadFile(filepath.Join(s.Dir, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		st.BackfillDone = map[int]bool{}
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return st, err
	}
	if st.BackfillDone == nil {
		st.BackfillDone = map[int]bool{}
	}
	return st, nil
}

func (s *Store) SaveState(st State) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(s.Dir, "state.json"), b)
}

func (s *Store) matchPath(id string) string {
	return filepath.Join(s.Dir, "matches", safe(id)+".json.gz")
}

func (s *Store) timelinePath(id string) string {
	return filepath.Join(s.Dir, "timelines", safe(id)+".json.gz")
}

func (s *Store) missingPath(id string) string {
	return filepath.Join(s.Dir, "timelines", safe(id)+".missing")
}

func (s *Store) HasMatch(id string) bool { return exists(s.matchPath(id)) }

// HasTimeline is true if the timeline is archived or known to be gone.
func (s *Store) HasTimeline(id string) bool {
	return exists(s.timelinePath(id)) || exists(s.missingPath(id))
}

func (s *Store) PutMatch(id string, raw []byte) error {
	return writeGzip(s.matchPath(id), raw)
}

func (s *Store) PutTimeline(id string, raw []byte) error {
	return writeGzip(s.timelinePath(id), raw)
}

func (s *Store) MarkTimelineMissing(id string) error {
	return writeAtomic(s.missingPath(id), nil)
}

func (s *Store) LoadMatch(id string) ([]byte, error) { return readGzip(s.matchPath(id)) }

// LoadTimeline returns os.ErrNotExist if there is no archived timeline.
func (s *Store) LoadTimeline(id string) ([]byte, error) { return readGzip(s.timelinePath(id)) }

// MatchIDs lists archived match ids, sorted.
func (s *Store) MatchIDs() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.Dir, "matches"))
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ".json.gz"); ok {
			ids = append(ids, name)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// PutRankSnapshot stores today's standings, one file per day.
func (s *Store) PutRankSnapshot(day time.Time, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(s.Dir, "ranks", day.Format("2006-01-02")+".json"), b)
}

// LatestRankSnapshot returns the newest snapshot, or nil if none.
func (s *Store) LatestRankSnapshot() ([]byte, string, error) {
	entries, err := os.ReadDir(filepath.Join(s.Dir, "ranks"))
	if err != nil {
		return nil, "", err
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return nil, "", nil
	}
	sort.Strings(names)
	last := names[len(names)-1]
	b, err := os.ReadFile(filepath.Join(s.Dir, "ranks", last))
	return b, strings.TrimSuffix(last, ".json"), err
}

func (s *Store) MetaPath(key string) string {
	return filepath.Join(s.Dir, "meta", safe(key)+".json")
}

func (s *Store) PutMeta(key string, b []byte) error { return writeAtomic(s.MetaPath(key), b) }

func safe(id string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
			return r
		}
		return '_'
	}, id)
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func writeGzip(path string, raw []byte) error {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return writeAtomic(path, buf.Bytes())
}

func readGzip(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}

func writeAtomic(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
