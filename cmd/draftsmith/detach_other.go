//go:build !windows

package main

import "errors"

// startDetached is Windows only: elsewhere the app runs in the foreground.
func startDetached(string, ...string) error { return errors.New("detaching needs Windows") }

const canDetach = false
