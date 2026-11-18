package db

import (
	"context"
	"database/sql"
	"time"
)

// MetaEntry is one cached OP.GG response. Body is the raw tool text, so a
// parser fix applies to cached responses too. Rebuilding the match index
// keeps these; a schema version bump drops them like everything else.
type MetaEntry struct {
	Key       string
	FetchedAt time.Time
	Patch     string // "" when the response doesn't say
	Body      string
}

// MetaEntry returns the cached response for key, or ErrNotFound.
func (d *DB) MetaEntry(ctx context.Context, key string) (MetaEntry, error) {
	e := MetaEntry{Key: key}
	var ms int64
	err := d.sql.QueryRowContext(ctx, `SELECT fetched_at, patch, body FROM meta_cache WHERE key = ?`, key).Scan(&ms, &e.Patch, &e.Body)
	if err == sql.ErrNoRows {
		return MetaEntry{}, ErrNotFound
	}
	if err != nil {
		return MetaEntry{}, err
	}
	e.FetchedAt = time.UnixMilli(ms)
	return e, nil
}

// PutMetaEntry stores or replaces a cached response.
func (d *DB) PutMetaEntry(ctx context.Context, e MetaEntry) error {
	_, err := d.sql.ExecContext(ctx, `INSERT INTO meta_cache (key, fetched_at, patch, body) VALUES (?, ?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET fetched_at = excluded.fetched_at, patch = excluded.patch, body = excluded.body`,
		e.Key, e.FetchedAt.UnixMilli(), e.Patch, e.Body)
	return err
}
