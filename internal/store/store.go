// Package store archives raw Riot JSON on disk as gzipped files.
//
// Layout under the data dir:
//
//	state.json                  the tracked accounts and their backfill progress
//	matches/<matchId>.json.gz   raw match-v5 responses, for every account
//	timelines/<matchId>.json.gz raw timeline responses
//	timelines/<matchId>.missing marker for timelines Riot no longer has
//	ranks/<puuid>/<date>.json   daily snapshot of an account's league-v4 standings
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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Store struct {
	Dir string
}

// State lists the tracked accounts, in the order they were added. They
// are all me: their games share one archive and one index.
type State struct {
	Accounts []Account `json:"accounts"`
}

// Account is one tracked Riot account.
type Account struct {
	PUUID    string `json:"puuid"`
	RiotID   string `json:"riotId"`
	Platform string `json:"platform"`
	// BackfillDone records, per queue, whether a full pass over the match id
	// list has completed. Until it has, sync pages all the way back instead
	// of stopping at the first page of already-archived games.
	BackfillDone map[int]bool `json:"backfillDone"`
	LastSync     time.Time    `json:"lastSync"`
}

// PUUIDs lists the tracked accounts' PUUIDs.
func (st State) PUUIDs() []string {
	out := make([]string, len(st.Accounts))
	for i, a := range st.Accounts {
		out[i] = a.PUUID
	}
	return out
}

// RiotIDs names the tracked accounts, e.g. "A#NA1, B#SLU".
func (st State) RiotIDs() string {
	names := make([]string, len(st.Accounts))
	for i, a := range st.Accounts {
		names[i] = a.RiotID
	}
	return strings.Join(names, ", ")
}

// Find returns the account with this PUUID, or nil.
func (st *State) Find(puuid string) *Account {
	for i := range st.Accounts {
		if st.Accounts[i].PUUID == puuid {
			return &st.Accounts[i]
		}
	}
	return nil
}

// LastSync is the most recent sync of any account.
func (st State) LastSync() time.Time {
	var t time.Time
	for _, a := range st.Accounts {
		if a.LastSync.After(t) {
			t = a.LastSync
		}
	}
	return t
}

// legacyState reads state.json from before multiple accounts too: one
// account at the top level, its rank snapshots directly under ranks/.
type legacyState struct {
	Account
	Accounts []Account `json:"accounts"`
}

func Open(dir string) (*Store, error) {
	for _, sub := range []string{"matches", "timelines", "ranks"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, err
		}
	}
	return &Store{Dir: dir}, nil
}

// LoadState reads the tracked accounts. A single-account state.json from
// before multiple accounts is migrated on the way: its account becomes the
// first, and its rank snapshots move under ranks/<puuid>/.
func (s *Store) LoadState() (State, error) {
	var st State
	b, err := os.ReadFile(filepath.Join(s.Dir, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	var raw legacyState
	if err := json.Unmarshal(b, &raw); err != nil {
		return st, err
	}
	st.Accounts = raw.Accounts
	if len(st.Accounts) == 0 && raw.PUUID != "" {
		st.Accounts = []Account{raw.Account}
		if err := s.migrateRanks(raw.PUUID); err != nil {
			return st, fmt.Errorf("moving rank snapshots: %w", err)
		}
		if err := s.SaveState(st); err != nil {
			return st, err
		}
	}
	for i := range st.Accounts {
		if st.Accounts[i].BackfillDone == nil {
			st.Accounts[i].BackfillDone = map[int]bool{}
		}
	}
	return st, nil
}

// migrateRanks moves rank snapshots from ranks/ into ranks/<puuid>/.
func (s *Store) migrateRanks(puuid string) error {
	entries, err := os.ReadDir(filepath.Join(s.Dir, "ranks"))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.ranksDir(puuid), 0o755); err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if err := os.Rename(filepath.Join(s.Dir, "ranks", e.Name()), filepath.Join(s.ranksDir(puuid), e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// stateMu serializes UpdateState across every Store in the process: a
// background sync and the settings page both change the tracked accounts.
var stateMu sync.Mutex

// UpdateState loads the state, lets f change it, and saves it, with no
// other UpdateState in between. Nothing is saved if f fails.
func (s *Store) UpdateState(f func(*State) error) (State, error) {
	stateMu.Lock()
	defer stateMu.Unlock()
	st, err := s.LoadState()
	if err != nil {
		return st, err
	}
	if err := f(&st); err != nil {
		return st, err
	}
	return st, s.SaveState(st)
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

// TimelineArchived is true only if the timeline itself is stored.
func (s *Store) TimelineArchived(id string) bool { return exists(s.timelinePath(id)) }

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

func (s *Store) ranksDir(puuid string) string {
	return filepath.Join(s.Dir, "ranks", safe(puuid))
}

// PutRankSnapshot stores an account's standings for a day, one file per
// day.
func (s *Store) PutRankSnapshot(puuid string, day time.Time, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.ranksDir(puuid), 0o755); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(s.ranksDir(puuid), day.Format("2006-01-02")+".json"), b)
}

// LatestRankSnapshot returns an account's newest snapshot, or nil if none.
func (s *Store) LatestRankSnapshot(puuid string) ([]byte, string, error) {
	days, err := s.RankSnapshotDays(puuid)
	if err != nil || len(days) == 0 {
		return nil, "", err
	}
	last := days[len(days)-1]
	b, err := s.LoadRankSnapshot(puuid, last)
	return b, last, err
}

// RankSnapshotDays lists the days (YYYY-MM-DD) with a rank snapshot for an
// account, oldest first.
func (s *Store) RankSnapshotDays(puuid string) ([]string, error) {
	entries, err := os.ReadDir(s.ranksDir(puuid))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var days []string
	for _, e := range entries {
		if day, ok := strings.CutSuffix(e.Name(), ".json"); ok {
			days = append(days, day)
		}
	}
	sort.Strings(days)
	return days, nil
}

// LoadRankSnapshot reads an account's snapshot for day (YYYY-MM-DD).
func (s *Store) LoadRankSnapshot(puuid, day string) ([]byte, error) {
	return os.ReadFile(filepath.Join(s.ranksDir(puuid), safe(day)+".json"))
}

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
