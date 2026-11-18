// Package riot is a small client for the parts of the Riot Games API that
// draftsmith needs: account-v1, match-v5 and league-v4.
package riot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrNotFound is returned for 404s, which match-v5 uses for timelines that
// have aged out (timelines are kept about a year, matches about two).
var ErrNotFound = errors.New("riot: not found")

// ErrAuth means the key was rejected. Retrying will not help.
var ErrAuth = errors.New("riot: API key rejected (expired or invalid? dev keys last 24h)")

type Client struct {
	key      string
	http     *http.Client
	limiter  *Limiter
	platform string // e.g. na1
	region   string // e.g. americas

	// BaseURL maps a host like "americas.api.riotgames.com" to a base URL.
	// Overridden in tests.
	BaseURL func(host string) string
	// MaxRetries for 429 and 5xx responses.
	MaxRetries int
	// Sleep is overridable in tests.
	Sleep func(ctx context.Context, d time.Duration) error
}

func NewClient(key, platform string, limits []Window) (*Client, error) {
	platform = strings.ToLower(platform)
	region, ok := RegionFor(platform)
	if !ok {
		return nil, fmt.Errorf("unknown platform %q (try na1, euw1, kr, ...)", platform)
	}
	return &Client{
		key:        key,
		http:       &http.Client{Timeout: 30 * time.Second},
		limiter:    NewLimiter(limits),
		platform:   platform,
		region:     region,
		BaseURL:    func(host string) string { return "https://" + host },
		MaxRetries: 4,
		Sleep:      sleepCtx,
	}, nil
}

func (c *Client) Platform() string { return c.platform }
func (c *Client) Region() string   { return c.region }

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (c *Client) regionalURL(path string) string {
	return c.BaseURL(c.region+".api.riotgames.com") + path
}

func (c *Client) platformURL(path string) string {
	return c.BaseURL(c.platform+".api.riotgames.com") + path
}

// getRaw performs a rate limited GET with retries and returns the body.
func (c *Client) getRaw(ctx context.Context, u string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Riot-Token", c.key)
		req.Header.Set("Accept", "application/json")

		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			if err := c.Sleep(ctx, backoff(attempt)); err != nil {
				return nil, err
			}
			continue
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()

		switch {
		case resp.StatusCode == http.StatusOK:
			if readErr != nil {
				return nil, readErr
			}
			return body, nil
		case resp.StatusCode == http.StatusNotFound:
			return nil, ErrNotFound
		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
			return nil, fmt.Errorf("%w: %d from %s", ErrAuth, resp.StatusCode, redact(u))
		case resp.StatusCode == http.StatusTooManyRequests:
			wait := backoff(attempt)
			if s := resp.Header.Get("Retry-After"); s != "" {
				if secs, err := strconv.Atoi(s); err == nil {
					wait = time.Duration(secs)*time.Second + 100*time.Millisecond
				}
			}
			lastErr = fmt.Errorf("riot: rate limited (%s)", resp.Header.Get("X-Rate-Limit-Type"))
			if err := c.Sleep(ctx, wait); err != nil {
				return nil, err
			}
		case resp.StatusCode >= 500:
			lastErr = fmt.Errorf("riot: %d from %s", resp.StatusCode, redact(u))
			if err := c.Sleep(ctx, backoff(attempt)); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("riot: %d from %s: %s", resp.StatusCode, redact(u), truncate(string(body), 200))
		}
	}
	return nil, fmt.Errorf("riot: giving up after %d attempts: %w", c.MaxRetries+1, lastErr)
}

func (c *Client) getJSON(ctx context.Context, u string, out any) error {
	b, err := c.getRaw(ctx, u)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func backoff(attempt int) time.Duration {
	d := time.Second << attempt
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}

func redact(u string) string {
	if i := strings.IndexByte(u, '?'); i >= 0 {
		return u[:i]
	}
	return u
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// Account is the account-v1 response.
type Account struct {
	PUUID    string `json:"puuid"`
	GameName string `json:"gameName"`
	TagLine  string `json:"tagLine"`
}

// AccountByRiotID resolves "Name#TAG" to a PUUID.
func (c *Client) AccountByRiotID(ctx context.Context, gameName, tagLine string) (Account, error) {
	var a Account
	u := c.regionalURL("/riot/account/v1/accounts/by-riot-id/" + url.PathEscape(gameName) + "/" + url.PathEscape(tagLine))
	err := c.getJSON(ctx, u, &a)
	return a, err
}

// MatchIDs lists match ids newest first. count is capped at 100 by Riot.
func (c *Client) MatchIDs(ctx context.Context, puuid string, queue, start, count int) ([]string, error) {
	q := url.Values{}
	if queue > 0 {
		q.Set("queue", strconv.Itoa(queue))
	}
	q.Set("start", strconv.Itoa(start))
	q.Set("count", strconv.Itoa(count))
	u := c.regionalURL("/lol/match/v5/matches/by-puuid/" + url.PathEscape(puuid) + "/ids?" + q.Encode())
	var ids []string
	err := c.getJSON(ctx, u, &ids)
	return ids, err
}

// MatchRaw returns the raw match-v5 JSON so it can be archived untouched.
func (c *Client) MatchRaw(ctx context.Context, matchID string) ([]byte, error) {
	return c.getRaw(ctx, c.regionalURL("/lol/match/v5/matches/"+url.PathEscape(matchID)))
}

// TimelineRaw returns the raw timeline JSON.
func (c *Client) TimelineRaw(ctx context.Context, matchID string) ([]byte, error) {
	return c.getRaw(ctx, c.regionalURL("/lol/match/v5/matches/"+url.PathEscape(matchID)+"/timeline"))
}

// LeagueEntry is one ranked queue standing from league-v4.
type LeagueEntry struct {
	QueueType    string `json:"queueType"`
	Tier         string `json:"tier"`
	Rank         string `json:"rank"`
	LeaguePoints int    `json:"leaguePoints"`
	Wins         int    `json:"wins"`
	Losses       int    `json:"losses"`
}

// LeagueEntries returns current ranked standings. Riot only exposes the
// current split here, which is why sync snapshots it every run.
func (c *Client) LeagueEntries(ctx context.Context, puuid string) ([]LeagueEntry, error) {
	var out []LeagueEntry
	err := c.getJSON(ctx, c.platformURL("/lol/league/v4/entries/by-puuid/"+url.PathEscape(puuid)), &out)
	return out, err
}
