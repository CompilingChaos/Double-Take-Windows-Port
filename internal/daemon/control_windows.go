//go:build windows

package daemon

import "os"

func controlNetwork() string { return "tcp" }

// A TCP listener already provides exclusive ownership of the configured
// endpoint, so Windows needs no filesystem lock or stale-socket cleanup.
func acquireInstanceLock(string) (*os.File, error) { return nil, nil }
func releaseInstanceLock(*os.File)                 {}
func removeStaleSocket(string) error               { return nil }
