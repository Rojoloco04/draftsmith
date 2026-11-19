//go:build !windows

package overlay

import "context"

// Overlay is the overlay window; it only exists on Windows.
type Overlay struct{}

// Start returns ErrUnsupported: the overlay needs Windows.
func Start(context.Context, Options) (*Overlay, error) { return nil, ErrUnsupported }

// SetActive does nothing.
func (*Overlay) SetActive(bool) {}
