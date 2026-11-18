//go:build !windows

package netdns

import "context"

// Status reports that the DNS switch is unavailable on this platform.
func Status(context.Context) (State, error) { return StateUnavailable, ErrUnsupported }

// Enable is not supported on this platform.
func Enable(context.Context) error { return ErrUnsupported }

// Disable is not supported on this platform.
func Disable(context.Context) error { return ErrUnsupported }
