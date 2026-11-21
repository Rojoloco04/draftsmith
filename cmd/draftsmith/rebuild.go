package main

import (
	"bufio"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// rebuiltEnv is set on a process started after a rebuild check, so it
// doesn't check again.
const rebuiltEnv = "DRAFTSMITH_REBUILT"

const modulePath = "github.com/rojoloco04/draftsmith"

// exePath is this executable, symlinks resolved.
func exePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// rebuildIfChanged runs when the exe starts with no command from its
// source checkout. It builds the source to a scratch file and, only if
// that differs from the running exe, swaps it in (the running exe moves
// aside to <name>~, since Windows can't overwrite a running program) and
// reports true: the caller then starts the new build. An unchanged build
// is thrown away, so nothing is renamed. Anything that stops it (no Go on
// PATH, a compile error) is reported and the current binary carries on,
// so a broken edit never stops a game.
func rebuildIfChanged(out io.Writer) bool {
	if os.Getenv(rebuiltEnv) != "" {
		return false
	}
	exe, err := exePath()
	if err != nil {
		return false
	}
	dir := filepath.Dir(exe)
	if !isCheckout(dir) {
		return false
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		fmt.Fprintln(out, "not checking for code changes: Go isn't on PATH")
		return false
	}

	fmt.Fprintln(out, "checking for code changes...")
	fresh := strings.TrimSuffix(exe, ".exe") + ".new.exe"
	defer os.Remove(fresh) // gone once renamed, or if anything fails
	build := exec.Command(goBin, "build", "-o", fresh, "./cmd/draftsmith")
	build.Dir = dir
	if msg, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(out, "rebuild failed, running the existing build:\n%s\n", strings.TrimSpace(string(msg)))
		return false
	}
	before, err1 := fileHash(exe)
	after, err2 := fileHash(fresh)
	if err1 != nil || err2 != nil || before == after {
		return false
	}
	old := exe + "~"
	os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		fmt.Fprintf(out, "rebuilt, but couldn't move the running exe aside: %v\n", err)
		return false
	}
	if err := os.Rename(fresh, exe); err != nil {
		os.Rename(old, exe)
		fmt.Fprintf(out, "rebuilt, but couldn't put the new exe in place: %v\n", err)
		return false
	}
	fmt.Fprintln(out, "code changed: rebuilt, starting the new version")
	return true
}

// removeOldExe deletes the <name>~ a rebuild left. The old process may
// still be running it for a moment (Windows won't delete a running exe),
// so it keeps trying for a few seconds in the background.
func removeOldExe() {
	exe, err := exePath()
	if err != nil {
		return
	}
	old := exe + "~"
	go func() {
		for range 20 {
			if err := os.Remove(old); err == nil || os.IsNotExist(err) {
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
	}()
}

// isCheckout reports whether dir is Draftsmith's source tree.
func isCheckout(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "cmd", "draftsmith")); err != nil {
		return false
	}
	f, err := os.Open(filepath.Join(dir, "go.mod"))
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if mod, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "module "); ok {
			return strings.TrimSpace(mod) == modulePath
		}
	}
	return false
}

func fileHash(path string) ([sha256.Size]byte, error) {
	var sum [sha256.Size]byte
	f, err := os.Open(path)
	if err != nil {
		return sum, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return sum, err
	}
	copy(sum[:], h.Sum(nil))
	return sum, nil
}
