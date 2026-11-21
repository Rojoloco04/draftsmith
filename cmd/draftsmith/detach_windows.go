//go:build windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

const (
	detachedProcess       = 0x00000008
	createNewProcessGroup = 0x00000200
)

// startDetached starts exe with no console window (and none to inherit),
// so the app runs in the background and the terminal or double-click
// console that started it can close.
func startDetached(exe string, env ...string) error {
	cmd := exec.Command(exe)
	cmd.Dir, _ = os.Getwd()
	cmd.Env = append(os.Environ(), env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | createNewProcessGroup, HideWindow: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// canDetach reports whether startDetached works here.
const canDetach = true
