package web

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// The dark tokens appear twice in style.css: under the system's dark mode
// and under a dark theme picked in settings. They must match.
func TestDarkThemeBlocksMatch(t *testing.T) {
	b, err := staticFS.ReadFile("static/style.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	block := func(re string) []string {
		m := regexp.MustCompile(`(?s)` + re + `\s*\{(.*?)\}`).FindStringSubmatch(css)
		if m == nil {
			t.Fatalf("no block for %s", re)
		}
		var out []string
		for _, d := range strings.Split(m[1], ";") {
			if d = strings.TrimSpace(d); d != "" && !strings.HasPrefix(d, "color-scheme") {
				out = append(out, d)
			}
		}
		return out
	}
	system := block(`:root:not\(\[data-theme="light"\]\)`)
	picked := block(`:root\[data-theme="dark"\]`)
	if strings.Join(system, ";") != strings.Join(picked, ";") {
		t.Errorf("dark tokens differ:\nsystem %v\npicked %v", system, picked)
	}
	if len(system) < 20 {
		t.Errorf("only %d dark tokens found", len(system))
	}
}

func TestThemeSetting(t *testing.T) {
	srv, _, ts := newEmptyApp(t)
	srv.SetAccounts([]Account{{PUUID: "p1", RiotID: "Me#NA1"}})

	_, body := get(t, ts, "/tier-list")
	expect(t, body, `<html lang="en">`, `<meta name="color-scheme" content="light dark">`)

	_, body = get(t, ts, "/settings")
	expect(t, body, `<option value="" selected>Match Windows</option>`)

	_, _, hdr := postForm(t, ts, "/settings/options", url.Values{"platform": {"na1"}, "queues": {"solo"}, "theme": {"light"}}, true)
	if hdr.Get("HX-Refresh") != "true" {
		t.Error("a new theme should reload the page")
	}
	_, body = get(t, ts, "/tier-list")
	expect(t, body, `<html lang="en" data-theme="light">`, `content="light"`)
	_, body = get(t, ts, "/overlay")
	expect(t, body, `data-theme="light"`)

	// Saving the same theme again doesn't reload.
	_, _, hdr = postForm(t, ts, "/settings/options", url.Values{"platform": {"na1"}, "queues": {"solo"}, "theme": {"light"}}, true)
	if hdr.Get("HX-Refresh") != "" {
		t.Error("unchanged theme reloaded the page")
	}
	// Anything else means the system's.
	postForm(t, ts, "/settings/options", url.Values{"platform": {"na1"}, "queues": {"solo"}, "theme": {"<script>"}}, true)
	if srv.Theme() != "" {
		t.Errorf("theme %q", srv.Theme())
	}
}
