package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// fakeSettings is a SettingsStore in memory.
type fakeSettings struct {
	s       Settings
	srv     *Server
	keyErr  error
	removed string
}

func (f *fakeSettings) Settings() Settings { return f.s }

func (f *fakeSettings) SetKey(_ context.Context, key string) error {
	if f.keyErr != nil {
		return f.keyErr
	}
	f.s.KeySet, f.s.KeyHint = true, key[len(key)-4:]
	return nil
}

func (f *fakeSettings) SetOptions(_ context.Context, platform, queues string, overlay bool) error {
	f.s.Platform, f.s.Queues, f.s.Overlay = platform, queues, overlay
	return nil
}

func (f *fakeSettings) AddAccount(_ context.Context, riotID string) (string, error) {
	if !strings.Contains(riotID, "#") {
		return "", errors.New("riot id should look like Name#TAG")
	}
	f.s.Accounts = append(f.s.Accounts, SettingsAccount{PUUID: "p-" + riotID, RiotID: riotID, Platform: "na1"})
	f.srv.SetAccounts([]Account{{PUUID: "p-" + riotID, RiotID: riotID}})
	f.srv.SetStatus(func(s *Status) { s.FirstSync = true })
	return riotID, nil
}

func (f *fakeSettings) RemoveAccount(_ context.Context, puuid string) error {
	f.removed = puuid
	return nil
}

// newEmptyApp is a server with no accounts yet and settings on.
func newEmptyApp(t *testing.T) (*Server, *fakeSettings, *httptest.Server) {
	t.Helper()
	srv, ts := newTestApp(t, nil)
	srv.SetAccounts(nil)
	f := &fakeSettings{s: Settings{Platform: "na1", Queues: "solo", Overlay: true, File: `C:\draftsmith\.env`}, srv: srv}
	srv.SetSettings(f)
	return srv, f, ts
}

func postForm(t *testing.T, ts *httptest.Server, path string, form url.Values, htmx bool) (int, string, http.Header) {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func TestNoAccountsGoesToSettings(t *testing.T) {
	_, _, ts := newEmptyApp(t)
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, p := range []string{"/", "/matches", "/champions"} {
		resp, err := noFollow.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/settings" {
			t.Errorf("%s with no accounts: %d to %q", p, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
	// The tier list doesn't need an archive.
	if code, _ := get(t, ts, "/tier-list"); code != http.StatusOK {
		t.Errorf("tier list with no accounts: %d", code)
	}
	code, body := get(t, ts, "/settings")
	if code != http.StatusOK {
		t.Fatalf("settings: %d", code)
	}
	expect(t, body, "Welcome to Draftsmith", `hx-post="/settings/key"`, "RGAPI-", `<option value="euw1">Europe West</option>`,
		`name="riot_id"`, `title="Save a key first"`, `C:\draftsmith\.env`)
}

func TestSettingsKeyThenFirstAccount(t *testing.T) {
	srv, f, ts := newEmptyApp(t)

	// Only from htmx: a cross-site form can't set the header.
	if code, _, _ := postForm(t, ts, "/settings/key", url.Values{"key": {"RGAPI-abcd-1234"}}, false); code != http.StatusForbidden || f.s.KeySet {
		t.Fatalf("plain POST: %d, key set %v", code, f.s.KeySet)
	}

	f.keyErr = errors.New("Riot rejected that key")
	_, body, _ := postForm(t, ts, "/settings/key", url.Values{"key": {"RGAPI-abcd-1234"}}, true)
	expect(t, body, `class="set-msg neg"`, "Riot rejected that key.")

	f.keyErr = nil
	_, body, _ = postForm(t, ts, "/settings/key", url.Values{"key": {` "RGAPI-abcd-1234" `}}, true)
	expect(t, body, "Riot accepted the key. Now add your Riot ID below.", "ends in 1234")
	if strings.Contains(body, "<html") {
		t.Error("a settings change answers with the block, not the page")
	}

	_, body, _ = postForm(t, ts, "/settings/accounts", url.Values{"riot_id": {"nope"}}, true)
	expect(t, body, "Riot id should look like Name#TAG.")

	_, _, hdr := postForm(t, ts, "/settings/accounts", url.Values{"riot_id": {"Me#NA1"}}, true)
	if hdr.Get("HX-Redirect") != "/" {
		t.Errorf("first account should go to the dashboard, got HX-Redirect %q", hdr.Get("HX-Redirect"))
	}

	// The first sync shows its progress instead of an empty dashboard,
	// and the poll reloads the page once it's done.
	srv.SetStatus(func(s *Status) { s.Syncing, s.Stage, s.Done, s.Total = true, "Fetching Me#NA1's games", 40, 400 })
	_, body = get(t, ts, "/")
	expect(t, body, "Forging your archive", "Fetching Me#NA1&#39;s games", "<strong>40</strong> of 400 games", `hx-get="/first-sync"`, `aria-valuenow="10"`)
	body, hdr2 := getHeader(t, ts, "/first-sync")
	if hdr2.Get("HX-Refresh") != "" {
		t.Error("no reload while the first sync runs")
	}
	srv.SetStatus(func(s *Status) { s.FirstSync, s.Syncing = false, false })
	_, hdr2 = getHeader(t, ts, "/first-sync")
	if hdr2.Get("HX-Refresh") != "true" {
		t.Error("the poll after the first sync should reload the page")
	}
}

func TestSettingsOptionsAndRemove(t *testing.T) {
	_, f, ts := newEmptyApp(t)
	f.s.KeySet = true
	f.s.Accounts = []SettingsAccount{{PUUID: "p1", RiotID: "Me#NA1", Platform: "na1"}, {PUUID: "p2", RiotID: "Alt#EUW", Platform: "euw1"}}

	_, body := get(t, ts, "/settings")
	expect(t, body, "Me#NA1", "Alt#EUW", "not synced while the region is na1", `hx-confirm="Stop tracking Alt#EUW?`)

	_, body, _ = postForm(t, ts, "/settings/options", url.Values{"platform": {"euw1"}, "queues": {"both"}}, true)
	if f.s.Platform != "euw1" || f.s.Queues != "both" || f.s.Overlay {
		t.Errorf("options: %+v", f.s)
	}
	expect(t, body, "Saved.", `<option value="euw1" selected>`)

	postForm(t, ts, "/settings/accounts/remove", url.Values{"puuid": {"p2"}}, true)
	if f.removed != "p2" {
		t.Errorf("removed %q", f.removed)
	}
}

func TestKeyRejectedBanner(t *testing.T) {
	srv, ts := newTestApp(t, nil)
	srv.SetStatus(func(s *Status) { s.KeyRejected = true })
	_, body := get(t, ts, "/matches")
	expect(t, body, "Riot rejected your API key", `<a href="/settings">Paste a new one in Settings</a>`)
}

func TestSyncingControl(t *testing.T) {
	srv, ts := newTestApp(t, nil)
	srv.SetRefresh(func() bool { return true })
	srv.SetStatus(func(s *Status) { s.Syncing, s.Stage, s.Done, s.Total = true, "Fetching Tester#NA1's games", 12, 30 })
	_, body := get(t, ts, "/matches")
	expect(t, body, `class="btn syncing" role="status"`, "Syncing 12/30")
	if strings.Contains(body, "pill live") {
		t.Error("the old green pill is gone")
	}
}

func TestIsLoopback(t *testing.T) {
	for addr, want := range map[string]bool{"127.0.0.1:5000": true, "[::1]:80": true, "100.64.1.2:5000": false, "192.168.1.5:1": false} {
		r := &http.Request{RemoteAddr: addr}
		if got := isLoopback(r); got != want {
			t.Errorf("%s: %v", addr, got)
		}
	}
}

func TestStatusLeft(t *testing.T) {
	if (Status{Total: 10, Done: 1}).Left() != "" {
		t.Error("no estimate from one game")
	}
	if p := (Status{Total: 400, Done: 40}).Percent(); p != 10 {
		t.Errorf("percent %d", p)
	}
}
