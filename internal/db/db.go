// Package db keeps a SQLite index over the raw match archive for the
// dashboard.
//
// The gzipped JSON under data/ stays the source of truth. Everything here
// is derived from it and can be rebuilt at any time (draftsmith reindex),
// which also happens automatically when SchemaVersion changes.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// SchemaVersion is stored in PRAGMA user_version. Bump it whenever the
// schema or the derived values change; Open then drops and recreates every
// table and the next Index rebuilds them from the archive.
const SchemaVersion = 3 // 3: games and rank snapshots per account

// FileName is the database file inside the data directory.
const FileName = "draftsmith.db"

type DB struct {
	sql  *sql.DB
	path string
	// Rebuilt is true if Open created the schema from scratch, either
	// because the file was new or because the schema version changed.
	Rebuilt bool
}

const schema = `
CREATE TABLE settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE games (
	match_id           TEXT PRIMARY KEY,
	account            TEXT NOT NULL, -- PUUID of my account in the game
	start_ms           INTEGER NOT NULL,
	patch              TEXT NOT NULL,
	queue              INTEGER NOT NULL,
	remake             INTEGER NOT NULL,
	minutes            REAL NOT NULL,
	champion           TEXT NOT NULL,
	role               TEXT NOT NULL,
	opponent           TEXT NOT NULL,
	win                INTEGER NOT NULL,
	kills              INTEGER NOT NULL,
	deaths             INTEGER NOT NULL,
	assists            INTEGER NOT NULL,
	cs                 INTEGER NOT NULL,
	cs_per_min         REAL NOT NULL,
	gold_per_min       REAL NOT NULL,
	vision_per_min     REAL NOT NULL,
	control_wards      INTEGER NOT NULL,
	kill_participation REAL NOT NULL,
	damage_share       REAL NOT NULL,
	has_timeline       INTEGER NOT NULL,
	-- at10_*/at15_* are NULL when there is no timeline or the game ended
	-- before the mark; *_diff are only meaningful when *_has_opp is 1.
	at10_gold INTEGER, at10_cs INTEGER, at10_xp INTEGER,
	at10_gold_diff INTEGER, at10_cs_diff INTEGER, at10_xp_diff INTEGER, at10_has_opp INTEGER,
	at15_gold INTEGER, at15_cs INTEGER, at15_xp INTEGER,
	at15_gold_diff INTEGER, at15_cs_diff INTEGER, at15_xp_diff INTEGER, at15_has_opp INTEGER,
	deaths_before_15   INTEGER NOT NULL,
	deaths_enemy_side  INTEGER NOT NULL
);
CREATE INDEX games_start ON games (start_ms);
CREATE INDEX games_champion ON games (champion);

CREATE TABLE deaths (
	match_id TEXT NOT NULL REFERENCES games ON DELETE CASCADE,
	seq      INTEGER NOT NULL,
	minute   REAL NOT NULL,
	x        INTEGER NOT NULL,
	y        INTEGER NOT NULL,
	PRIMARY KEY (match_id, seq)
);

-- Every player's end-of-game line. No names, Riot IDs or PUUIDs: other
-- players are shown as champion + position only.
CREATE TABLE participants (
	match_id       TEXT NOT NULL REFERENCES games ON DELETE CASCADE,
	participant_id INTEGER NOT NULL,
	team_id        INTEGER NOT NULL,
	position       TEXT NOT NULL,
	champion       TEXT NOT NULL,
	me             INTEGER NOT NULL,
	win            INTEGER NOT NULL,
	level          INTEGER NOT NULL,
	kills          INTEGER NOT NULL,
	deaths         INTEGER NOT NULL,
	assists        INTEGER NOT NULL,
	cs             INTEGER NOT NULL,
	gold           INTEGER NOT NULL,
	damage         INTEGER NOT NULL,
	vision         INTEGER NOT NULL,
	items          TEXT NOT NULL, -- item0..item6, comma separated
	spells         TEXT NOT NULL, -- summoner1Id,summoner2Id
	-- primary style, 4 primary runes, sub style, 2 sub runes, 3 shards
	runes          TEXT NOT NULL,
	PRIMARY KEY (match_id, participant_id)
);

-- My and my lane opponent's totals per timeline frame.
CREATE TABLE frames (
	match_id TEXT NOT NULL REFERENCES games ON DELETE CASCADE,
	minute   REAL NOT NULL,
	gold     INTEGER NOT NULL,
	cs       INTEGER NOT NULL,
	xp       INTEGER NOT NULL,
	has_opp  INTEGER NOT NULL,
	opp_gold INTEGER NOT NULL,
	opp_cs   INTEGER NOT NULL,
	opp_xp   INTEGER NOT NULL,
	PRIMARY KEY (match_id, minute)
);

CREATE TABLE rank_snapshots (
	account  TEXT NOT NULL, -- PUUID
	day      TEXT NOT NULL, -- YYYY-MM-DD
	queue    TEXT NOT NULL, -- RANKED_SOLO_5x5, RANKED_FLEX_SR
	tier     TEXT NOT NULL,
	division TEXT NOT NULL,
	lp       INTEGER NOT NULL,
	wins     INTEGER NOT NULL,
	losses   INTEGER NOT NULL,
	PRIMARY KEY (account, day, queue)
);

-- OP.GG responses (filled in Phase 2).
CREATE TABLE meta_cache (
	key        TEXT PRIMARY KEY,
	fetched_at INTEGER NOT NULL, -- unix ms
	patch      TEXT NOT NULL,
	body       TEXT NOT NULL
);
`

// OpenDir opens (creating if needed) the database in a data directory.
func OpenDir(ctx context.Context, dataDir string) (*DB, error) {
	return Open(ctx, filepath.Join(dataDir, FileName))
}

// Open opens the database at path, creating or migrating the schema.
func Open(ctx context.Context, path string) (*DB, error) {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	s, err := sql.Open("sqlite", path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	d := &DB{sql: s, path: path}
	if err := d.migrate(ctx); err != nil {
		s.Close()
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	return d, nil
}

func (d *DB) Close() error { return d.sql.Close() }

// Path is the database file.
func (d *DB) Path() string { return d.path }

func (d *DB) migrate(ctx context.Context) error {
	var v int
	if err := d.sql.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	if v == SchemaVersion {
		return nil
	}
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return err
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		tables = append(tables, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	// Dropping a parent table before its children would fail an immediate
	// foreign key check. Deferred to commit, every table is gone by then.
	if _, err := tx.ExecContext(ctx, "PRAGMA defer_foreign_keys = ON"); err != nil {
		return err
	}
	for _, t := range tables {
		if _, err := tx.ExecContext(ctx, `DROP TABLE "`+t+`"`); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, schema); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", SchemaVersion)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	d.Rebuilt = true
	return nil
}

func (d *DB) setting(ctx context.Context, key string) (string, error) {
	var v string
	err := d.sql.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func (d *DB) setSetting(ctx context.Context, key, value string) error {
	_, err := d.sql.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
