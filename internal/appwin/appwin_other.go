//go:build !windows

package appwin

import "context"

// Run returns ErrUnsupported: the app window needs Windows.
func Run(context.Context, Options) error { return ErrUnsupported }

// Alert does nothing; callers print the message too.
func Alert(title, msg string) {}

// HideOwnConsole does nothing.
func HideOwnConsole() bool { return false }

// MarkOwnConsole does nothing.
func MarkOwnConsole() {}
