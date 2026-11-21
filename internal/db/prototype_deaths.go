package db

// PROTOTYPE: throwaway query for the overview redesign prototypes
// (internal/web/prototype_overview.go). Delete or fold in properly, with
// a test, once a design is picked.

import "context"

// ProtoDeath is one of my deaths with the side I played on.
type ProtoDeath struct {
	MatchID string
	Minute  float64
	X, Y    int
	Blue    bool // my team was 100 (blue, bottom left)
}

// ProtoDeaths returns every indexed death of mine, all games.
func (d *DB) ProtoDeaths(ctx context.Context) ([]ProtoDeath, error) {
	rows, err := d.sql.QueryContext(ctx, `SELECT d.match_id, d.minute, d.x, d.y, coalesce(p.team_id, 100)
		FROM deaths d LEFT JOIN participants p ON p.match_id = d.match_id AND p.me = 1
		ORDER BY d.match_id, d.seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProtoDeath
	for rows.Next() {
		var p ProtoDeath
		var team int
		if err := rows.Scan(&p.MatchID, &p.Minute, &p.X, &p.Y, &team); err != nil {
			return nil, err
		}
		p.Blue = team != 200
		out = append(out, p)
	}
	return out, rows.Err()
}
