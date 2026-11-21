package main

import (
	"bufio"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
)

// rebuiltEnv is set on the child a rebuild starts, so it doesn't check again.
const rebuiltEnv = "DRAFTSMITH_REBUILT"

const modulePath = "github.com/rojoloco04/draftsmith"

// rebuildIfChanged runs when the exe starts with no arguments from its
// source checkout. It runs `go build` over itself, and if that changes the
// binary, runs the new one with the same arguments and exits with its code.
// Anything that stops it (no Go on PATH, a compile error) is reported and
// the current binary carries on, so a broken edit never stops a game.
func rebuildIfChanged(out io.Writer) {
	if os.Getenv(rebuiltEnv) != "" {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return
	}
	dir := filepath.Dir(exe)
	// Go renames a running exe to <name>~ to replace it; the last run's
	// copy can go now that nothing runs it.
	os.Remove(exe + "~")
	if !isCheckout(dir) {
		return
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		fmt.Fprintln(out, "not checking for code changes: Go isn't on PATH")
		return
	}

	before, err := fileHash(exe)
	if err != nil {
		return
	}
	fmt.Fprintln(out, "checking for code changes...")
	build := exec.Command(goBin, "build", "-o", exe, "./cmd/draftsmith")
	build.Dir = dir
	if msg, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(out, "rebuild failed, running the existing build:\n%s\n", strings.TrimSpace(string(msg)))
		return
	}
	after, err := fileHash(exe)
	if err != nil || after == before {
		return
	}

	fmt.Fprintln(out, "code changed: rebuilt, starting the new version")
	child := exec.Command(exe, os.Args[1:]...)
	child.Dir, _ = os.Getwd()
	child.Env = append(os.Environ(), rebuiltEnv+"=1")
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	// Ctrl+C reaches the child too (same console); let it shut down and
	// exit with it rather than dying first.
	signal.Ignore(os.Interrupt)
	err = child.Run()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		os.Exit(exit.ExitCode())
	case err != nil:
		fmt.Fprintln(out, "error: starting the rebuilt exe:", err)
		waitForEnter()
		os.Exit(1)
	}
	os.Exit(0)
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
