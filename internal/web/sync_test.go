package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func post(t *testing.T, ts *httptest.Server, path string, hdr ...string) (int, string, http.Header) {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.URL+path, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func getHeader(t *testing.T, ts *httptest.Server, path string) (string, http.Header) {
	t.Helper()
	req, _ := http.NewRequest("GET", ts.URL+path, nil)
	req.Header.Set("HX-Request", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b), resp.Header
}

func TestRefreshOffWithoutSync(t *testing.T) {
	_, ts := newTestApp(t, nil)
	_, body := get(t, ts, "/")
	if strings.Contains(body, `hx-post="/sync"`) {
		t.Error("no Refresh button without background sync")
	}
	if code, _, _ := post(t, ts, "/sync", "HX-Request", "true"); code != http.StatusConflict {
		t.Errorf("POST /sync with sync off: %d", code)
	}
}

func TestRefreshButton(t *testing.T) {
	srv, ts := newTestApp(t, nil)
	calls := 0
	srv.SetRefresh(func() bool { calls++; return true })

	_, body := get(t, ts, "/matches")
	expect(t, body, `<button type="button" class="btn" hx-post="/sync"`, "Refresh</span></button>")

	// Not from htmx (a cross-site form, say): refused, nothing started.
	if code, _, _ := post(t, ts, "/sync"); code != http.StatusForbidden || calls != 0 {
		t.Errorf("plain POST: %d, %d calls", code, calls)
	}

	code, body, _ := post(t, ts, "/sync", "HX-Request", "true")
	if code != http.StatusOK || calls != 1 {
		t.Fatalf("POST /sync: %d, %d calls", code, calls)
	}
	expect(t, body, "Syncing…", `hx-get="/sync?wait=1"`, `hx-trigger="every 1s"`)
	if strings.Contains(body, "<html") {
		t.Error("POST /sync should return only the control")
	}

	// While it runs, more clicks don't queue more syncs, and the poll
	// keeps waiting.
	post(t, ts, "/sync", "HX-Request", "true")
	if calls != 1 {
		t.Errorf("refresh called %d times during one sync", calls)
	}
	body, hdr := getHeader(t, ts, "/sync?wait=1")
	if hdr.Get("HX-Refresh") != "" || !strings.Contains(body, "Syncing…") {
		t.Errorf("mid sync: HX-Refresh %q, body %s", hdr.Get("HX-Refresh"), body)
	}
	// Pages loaded meanwhile show the sync and poll without reloading.
	_, page := get(t, ts, "/")
	expect(t, page, `hx-get="/sync"`, "Syncing…")

	// Once it's done, the clicked page reloads to show new games; a page
	// that only watched just gets its button back.
	srv.SetStatus(func(s *Status) { s.Syncing = false })
	body, hdr = getHeader(t, ts, "/sync?wait=1")
	if hdr.Get("HX-Refresh") != "true" {
		t.Errorf("finished sync should reload the page, headers %v", hdr)
	}
	expect(t, body, "Refresh</span></button>")
	if _, hdr = getHeader(t, ts, "/sync"); hdr.Get("HX-Refresh") != "" {
		t.Error("a page that didn't click shouldn't reload")
	}

	// A refresh that couldn't be queued doesn't show a sync that isn't
	// coming.
	srv.SetRefresh(func() bool { return false })
	_, body, _ = post(t, ts, "/sync", "HX-Request", "true")
	if strings.Contains(body, "Syncing…") {
		t.Errorf("unqueued refresh shown as syncing: %s", body)
	}
}
