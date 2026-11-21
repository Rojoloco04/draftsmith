// Package liveclient reads my own champion's state from Riot's Live Client
// Data API, the HTTPS server the game runs on 127.0.0.1:2999 while a game
// is loaded: my level, skill points and items.
//
// It asks only for the active player (me). Other players' data is never
// requested. My Riot ID is needed to ask for my items; it's kept in memory
// for that and never logged or shown.
package liveclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/rojoloco04/draftsmith/internal/lcu"
)

// DefaultBase is where the game serves the API.
const DefaultBase = "https://127.0.0.1:2999"

// ErrNotReady means the game isn't serving data yet (loading screen) or
// at all (no game running).
var ErrNotReady = errors.New("liveclient: game data not available yet")

// Item is one item in my inventory.
type Item struct {
	ID    int    `json:"itemID"`
	Name  string `json:"displayName"`
	Count int    `json:"count"`
	Slot  int    `json:"slot"` // 0-5 inventory, 6 trinket
}

// Snapshot is my champion's state at one moment.
type Snapshot struct {
	Level int
	// Skills are the points in each ability ("Q", "W", "E", "R").
	Skills map[string]int
	Items  []Item
}

// Spent is how many skill points are in abilities.
func (s Snapshot) Spent() int {
	n := 0
	for _, k := range []string{"Q", "W", "E", "R"} {
		n += s.Skills[k]
	}
	return n
}

// Has reports whether I own an item.
func (s Snapshot) Has(id int) bool {
	for _, it := range s.Items {
		if it.ID == id {
			return true
		}
	}
	return false
}

// Client reads the API.
type Client struct {
	base string
	http *http.Client
}

// New returns a client for the game on this PC. The game's certificate is
// issued by Riot's root, the same one the League client uses; it's
// verified against that root alone. Its names don't always cover
// 127.0.0.1, so the hostname isn't checked: the address is fixed to
// loopback, and the chain still has to lead to Riot's root.
func New() *Client {
	roots := lcu.RiotRoots()
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: true, // replaced by VerifyConnection below
			VerifyConnection: func(cs tls.ConnectionState) error {
				if len(cs.PeerCertificates) == 0 {
					return errors.New("liveclient: no certificate")
				}
				inter := x509.NewCertPool()
				for _, c := range cs.PeerCertificates[1:] {
					inter.AddCert(c)
				}
				_, err := cs.PeerCertificates[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter})
				return err
			},
		},
		Proxy:               nil, // loopback only
		MaxIdleConnsPerHost: 1,
	}
	return NewWith(DefaultBase, &http.Client{Transport: tr, Timeout: 3 * time.Second})
}

// NewWith is New with the address and HTTP client injected, for tests.
func NewWith(base string, hc *http.Client) *Client {
	return &Client{base: base, http: hc}
}

// Snapshot reads my level, skill points and items.
func (c *Client) Snapshot(ctx context.Context) (Snapshot, error) {
	type ability struct {
		Level int `json:"abilityLevel"`
	}
	var me struct {
		Level     int                `json:"level"`
		RiotID    string             `json:"riotId"`
		Abilities map[string]ability `json:"abilities"`
	}
	if err := c.get(ctx, "/liveclientdata/activeplayer", nil, &me); err != nil {
		return Snapshot{}, err
	}
	if me.RiotID == "" || me.Level == 0 {
		return Snapshot{}, ErrNotReady
	}
	s := Snapshot{Level: me.Level, Skills: map[string]int{}}
	for _, k := range []string{"Q", "W", "E", "R"} {
		s.Skills[k] = me.Abilities[k].Level
	}
	if err := c.get(ctx, "/liveclientdata/playeritems", url.Values{"riotId": {me.RiotID}}, &s.Items); err != nil {
		return Snapshot{}, err
	}
	return s, nil
}

func (c *Client) get(ctx context.Context, path string, q url.Values, v any) error {
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrNotReady // not listening: loading screen or no game
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusServiceUnavailable {
		io.Copy(io.Discard, resp.Body)
		return ErrNotReady
	}
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		// The path is ours; the query (my Riot ID) stays out of errors.
		return fmt.Errorf("liveclient GET %s: HTTP %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return fmt.Errorf("liveclient GET %s: %w", path, err)
	}
	return nil
}

// Follow reads a snapshot every interval until ctx ends, calling f with
// each one that differs from the last, or with the error when a read
// fails (ErrNotReady while the game loads). f is called from Follow's
// goroutine.
func (c *Client) Follow(ctx context.Context, interval time.Duration, f func(Snapshot, error)) {
	var last string
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		s, err := c.Snapshot(ctx)
		if ctx.Err() != nil {
			return
		}
		key := fmt.Sprintf("%+v|%v", s, err)
		if key != last {
			last = key
			f(s, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
