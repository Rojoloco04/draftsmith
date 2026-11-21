package lcu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// PageName is the one rune page Draftsmith keeps in the client; each
// import replaces it. Any page whose name starts with PagePrefix counts as
// Draftsmith's (including pages from builds that named them per
// champion), so there's only ever one. No other page is touched.
const (
	PageName   = "Draftsmith Imported"
	PagePrefix = "Draftsmith"
)

// RunePage is a rune page to put in the client.
type RunePage struct {
	PrimaryStyle int
	SubStyle     int
	// Perks are the four primary runes, two secondary runes and three
	// stat shards, in that order.
	Perks []int
}

// ErrNoRoom means every custom rune page is in use and none is
// Draftsmith's.
var ErrNoRoom = errors.New("no free rune page: delete one in the client, or rename one to start with " + PagePrefix + " so it can be reused")

// ImportRunes puts page in the client as the current rune page, named
// PageName. It replaces Draftsmith's page (any page whose name starts with
// PagePrefix) or, if there isn't one, adds it. It only ever runs when I
// click Import; nothing calls it on its own.
func (c *Client) ImportRunes(ctx context.Context, page RunePage) error {
	if len(page.Perks) != 9 || page.PrimaryStyle == 0 || page.SubStyle == 0 {
		return fmt.Errorf("incomplete rune page (%d runes)", len(page.Perks))
	}
	var pages []struct {
		ID          int    `json:"id"`
		Name        string `json:"name"`
		IsEditable  bool   `json:"isEditable"`
		IsDeletable bool   `json:"isDeletable"`
	}
	if err := c.Get(ctx, "/lol-perks/v1/pages", &pages); err != nil {
		return fmt.Errorf("reading rune pages: %w", err)
	}
	freed := false
	for _, p := range pages {
		if p.IsDeletable && strings.HasPrefix(p.Name, PagePrefix) {
			if err := c.send(ctx, http.MethodDelete, fmt.Sprintf("/lol-perks/v1/pages/%d", p.ID), nil); err != nil {
				return fmt.Errorf("replacing the old %s page: %w", PagePrefix, err)
			}
			freed = true
		}
	}
	if !freed {
		var inv struct {
			CanAdd *bool `json:"canAddCustomPage"`
		}
		if err := c.Get(ctx, "/lol-perks/v1/inventory", &inv); err == nil && inv.CanAdd != nil && !*inv.CanAdd {
			return ErrNoRoom
		}
	}
	body := map[string]any{
		"name":            PageName,
		"primaryStyleId":  page.PrimaryStyle,
		"subStyleId":      page.SubStyle,
		"selectedPerkIds": page.Perks,
		"current":         true,
	}
	err := c.send(ctx, http.MethodPost, "/lol-perks/v1/pages", body)
	var he *httpError
	if errors.As(err, &he) && he.code == http.StatusBadRequest && strings.Contains(strings.ToLower(he.msg), "max") {
		return ErrNoRoom
	}
	return err
}

// httpError is a non-2xx answer from the client.
type httpError struct {
	method, path string
	code         int
	msg          string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("lcu %s %s: HTTP %d: %s", e.method, e.path, e.code, e.msg)
}

// send makes a request with an optional JSON body and discards the answer.
func (c *Client) send(ctx context.Context, method, path string, body any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return err
	}
	req.SetBasicAuth("riot", c.password)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 == 2 {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		io.Copy(io.Discard, resp.Body)
		return ErrAuth
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
	var m struct {
		Message string `json:"message"`
	}
	msg := strings.TrimSpace(string(b))
	if json.Unmarshal(b, &m) == nil && m.Message != "" {
		msg = m.Message
	}
	return &httpError{method: method, path: path, code: resp.StatusCode, msg: msg}
}
