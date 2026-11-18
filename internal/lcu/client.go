package lcu

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// riotRoot is Riot's public CA for the LCU, from
// https://static.developer.riotgames.com/docs/lol/riotgames.pem. The
// client's certificate is issued by it for localhost and 127.0.0.1, so
// normal verification works with it as the only trusted root.
//
//go:embed riotgames.pem
var riotRoot []byte

// RiotRoots is a pool holding only Riot's LCU root certificate.
func RiotRoots() *x509.CertPool {
	p := x509.NewCertPool()
	if !p.AppendCertsFromPEM(riotRoot) {
		panic("lcu: bad embedded riotgames.pem")
	}
	return p
}

var (
	// ErrNotFound is a 404: in practice, asking for state the client isn't
	// in (the champ select session outside champ select).
	ErrNotFound = errors.New("lcu: not found")
	// ErrAuth is a 401 or 403: the credentials belong to an older session.
	ErrAuth = errors.New("lcu: credentials rejected")
)

// Client is a read-mostly LCU client for one client session.
type Client struct {
	base     string
	password string
	http     *http.Client
}

// New returns a client for creds that trusts only Riot's root.
func New(creds Credentials) *Client {
	tr := &http.Transport{
		TLSClientConfig:     &tls.Config{RootCAs: RiotRoots(), MinVersion: tls.VersionTLS12},
		Proxy:               nil, // localhost only; never route through a proxy
		MaxIdleConnsPerHost: 2,
	}
	return newClient("https://127.0.0.1:"+strconv.Itoa(creds.Port), creds.Password, &http.Client{Transport: tr, Timeout: 5 * time.Second})
}

func newClient(base, password string, hc *http.Client) *Client {
	return &Client{base: base, password: password, http: hc}
}

// Get decodes the JSON at path into v.
func (c *Client) Get(ctx context.Context, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth("riot", c.password)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		io.Copy(io.Discard, resp.Body)
		return ErrNotFound
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		io.Copy(io.Discard, resp.Body)
		return ErrAuth
	case resp.StatusCode != http.StatusOK:
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return fmt.Errorf("lcu GET %s: HTTP %d: %s", path, resp.StatusCode, b)
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return fmt.Errorf("lcu GET %s: %w", path, err)
	}
	return nil
}

// Gameflow phases the watcher cares about. The client has more (Matchmaking,
// ReadyCheck, Reconnect, PreEndOfGame, ...); they're passed through as is.
const (
	PhaseNone        = "None"
	PhaseLobby       = "Lobby"
	PhaseChampSelect = "ChampSelect"
	PhaseInProgress  = "InProgress"
)

// GameflowPhase returns the client's current phase, e.g. "Lobby".
func (c *Client) GameflowPhase(ctx context.Context) (string, error) {
	var p string
	err := c.Get(ctx, "/lol-gameflow/v1/gameflow-phase", &p)
	return p, err
}
