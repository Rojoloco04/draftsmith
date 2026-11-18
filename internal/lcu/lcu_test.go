package lcu

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const secret = "s3cr3t-Pw_xyz"

func TestParseLockfile(t *testing.T) {
	c, err := ParseLockfile([]byte("LeagueClient:20720:51325:" + secret + ":https\r\n"))
	if err != nil || c.Port != 51325 || c.PID != 20720 || c.Password != secret {
		t.Fatalf("%+v, %v", c, err)
	}
	for _, bad := range []string{"", "LeagueClient:1:2:pw", "LeagueClient:x:51325:pw:https", "LeagueClient:1:0:pw:https", "LeagueClient:1:51325::https", "LeagueClient:1:51325:pw:http"} {
		if _, err := ParseLockfile([]byte(bad)); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}

func TestParseCommandLine(t *testing.T) {
	// Shaped like the real one, which also carries Riot Client tokens.
	cmd := `"C:/Riot Games/League of Legends/LeagueClientUx.exe" "--riotclient-auth-token=other" "--riotclient-app-port=60616" ` +
		`"--remoting-auth-token=` + secret + `" "--app-port=51325" "--install-directory=C:\Riot Games\League of Legends" "--app-pid=20720"` + "\r\n"
	c, err := ParseCommandLine(cmd)
	if err != nil || c.Port != 51325 || c.PID != 20720 || c.Password != secret {
		t.Fatalf("%+v, %v", c, err)
	}
	if c, err := ParseCommandLine("LeagueClientUx.exe --app-port=1234 --remoting-auth-token=abc"); err != nil || c.Port != 1234 || c.Password != "abc" {
		t.Errorf("unquoted: %+v, %v", c, err)
	}
	if _, err := ParseCommandLine(`"--riotclient-app-port=60616" "--riotclient-auth-token=x"`); err == nil {
		t.Error("Riot Client's port and token must not be mistaken for the LCU's")
	}
}

func TestCredentialsStringHidesPassword(t *testing.T) {
	c := Credentials{Port: 1, Password: secret, PID: 2, Source: "lockfile"}
	if s := c.String(); strings.Contains(s, secret) {
		t.Errorf("String leaks the password: %s", s)
	}
}

func TestDiscoverLockfile(t *testing.T) {
	empty, dir := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lockfile"), []byte("LeagueClient:7:4242:"+secret+":https"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Discover(context.Background(), []string{empty, dir})
	if err != nil || c.Port != 4242 || c.Source != filepath.Join(dir, "lockfile") {
		t.Fatalf("%+v, %v", c, err)
	}
	if err := os.WriteFile(filepath.Join(empty, "lockfile"), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(context.Background(), []string{empty, dir}); err == nil {
		t.Error("a malformed lockfile should be an error, not skipped")
	}
}

func TestRiotRoot(t *testing.T) {
	// SHA-256 of the DER certificate in riotgames.pem, as published by Riot.
	const want = "ca8c9d325b4cdc464c6c94a585c85e91ec23d40ba5bf3ae2822b951a4a504ea3"
	block, rest := pem.Decode(riotRoot)
	if block == nil || len(strings.TrimSpace(string(rest))) != 0 {
		t.Fatal("riotgames.pem should hold exactly one certificate")
	}
	if sum := sha256.Sum256(block.Bytes); hex.EncodeToString(sum[:]) != want {
		t.Errorf("riotgames.pem fingerprint %x, want %s", sum, want)
	}
}

func TestClient(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "riot" || p != secret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/lol-gameflow/v1/gameflow-phase":
			w.Write([]byte(`"ChampSelect"`))
		case "/broken":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"errorCode":"RPC_ERROR","httpStatus":404,"message":"No active delegate"}`))
		}
	}))
	defer srv.Close()
	ctx := context.Background()

	c := newClient(srv.URL, secret, srv.Client())
	if p, err := c.GameflowPhase(ctx); err != nil || p != PhaseChampSelect {
		t.Errorf("phase %q, %v", p, err)
	}
	var v any
	if err := c.Get(ctx, "/lol-champ-select/v1/session", &v); !errors.Is(err, ErrNotFound) {
		t.Errorf("404: %v", err)
	}
	if err := c.Get(ctx, "/broken", &v); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("500: %v", err)
	}
	if _, err := newClient(srv.URL, "old", srv.Client()).GameflowPhase(ctx); !errors.Is(err, ErrAuth) {
		t.Errorf("wrong password: %v", err)
	}

	// The real client trusts only Riot's root, so a server with any other
	// certificate is refused.
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	if _, err := New(Credentials{Port: port, Password: secret}).GameflowPhase(ctx); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Errorf("want a certificate error, got %v", err)
	}
}
