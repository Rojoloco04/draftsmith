// Package lcu talks to the League Client API (LCU): the HTTPS server the
// League client runs on localhost while it's open.
//
// Credentials come from the client's lockfile, or failing that from the
// LeagueClientUx.exe command line. Both hold a per-session password; it is
// never logged, and nothing else from the command line is kept (it also
// carries Riot Client tokens).
package lcu

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// ErrNotRunning means no running League client was found.
var ErrNotRunning = errors.New("League client not running")

// Credentials locate and unlock one client session.
type Credentials struct {
	Port     int
	Password string
	PID      int    // 0 if unknown
	Source   string // where they came from, for logs
}

// String leaves the password out, so credentials are safe to log.
func (c Credentials) String() string {
	return fmt.Sprintf("port %d, pid %d, from %s", c.Port, c.PID, c.Source)
}

// DefaultDirs are the usual install directories.
func DefaultDirs() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{`C:\Riot Games\League of Legends`}
	case "darwin":
		return []string{"/Applications/League of Legends.app/Contents/LoL"}
	}
	return nil
}

// ParseLockfile reads "LeagueClient:pid:port:password:https".
func ParseLockfile(b []byte) (Credentials, error) {
	f := strings.Split(strings.TrimSpace(string(b)), ":")
	if len(f) != 5 {
		return Credentials{}, fmt.Errorf("lockfile: want 5 fields, got %d", len(f))
	}
	pid, err1 := strconv.Atoi(f[1])
	port, err2 := strconv.Atoi(f[2])
	if err1 != nil || err2 != nil || port <= 0 || port > 65535 {
		return Credentials{}, errors.New("lockfile: bad pid or port")
	}
	if f[3] == "" {
		return Credentials{}, errors.New("lockfile: empty password")
	}
	if f[4] != "https" {
		return Credentials{}, fmt.Errorf("lockfile: protocol %q, want https", f[4])
	}
	return Credentials{Port: port, Password: f[3], PID: pid}, nil
}

// ParseCommandLine pulls --app-port, --remoting-auth-token and --app-pid
// out of a LeagueClientUx.exe command line. Arguments may be quoted
// ("--app-port=51325") or bare.
func ParseCommandLine(cmd string) (Credentials, error) {
	var c Credentials
	for _, arg := range strings.Fields(cmd) {
		arg = strings.Trim(arg, `"`)
		name, val, ok := strings.Cut(arg, "=")
		if !ok {
			continue
		}
		switch name {
		case "--app-port":
			c.Port, _ = strconv.Atoi(val)
		case "--remoting-auth-token":
			c.Password = val
		case "--app-pid":
			c.PID, _ = strconv.Atoi(val)
		}
	}
	if c.Port <= 0 || c.Port > 65535 || c.Password == "" {
		return Credentials{}, errors.New("command line has no --app-port or --remoting-auth-token")
	}
	return c, nil
}

// Discover finds a running client: the lockfile in the first of dirs that
// has one, then the LeagueClientUx.exe process (Windows only). It returns
// ErrNotRunning if neither turns up.
func Discover(ctx context.Context, dirs []string) (Credentials, error) {
	for _, d := range dirs {
		path := filepath.Join(d, "lockfile")
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		c, err := ParseLockfile(b)
		if err != nil {
			return Credentials{}, fmt.Errorf("%s: %w", path, err)
		}
		c.Source = path
		return c, nil
	}
	cmd, err := processCommandLine(ctx)
	if err != nil {
		return Credentials{}, err
	}
	c, err := ParseCommandLine(cmd)
	if err != nil {
		return Credentials{}, err
	}
	c.Source = "LeagueClientUx.exe command line"
	return c, nil
}

// processCommandLine returns LeagueClientUx.exe's command line. Go can't
// read another process's arguments on its own, so this asks PowerShell.
// The output holds secrets, so it never goes into an error.
func processCommandLine(ctx context.Context) (string, error) {
	if runtime.GOOS != "windows" {
		return "", ErrNotRunning
	}
	out, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		`(Get-CimInstance Win32_Process -Filter "Name = 'LeagueClientUx.exe'" | Select-Object -First 1).CommandLine`).Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		// An *exec.ExitError's message is just the exit status.
		return "", fmt.Errorf("looking for LeagueClientUx.exe: %w", err)
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return "", ErrNotRunning
	}
	return string(out), nil
}
