//go:build !windows

package daemon

import (
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"
)

func controlNetwork() string { return "unix" }

func acquireInstanceLock(socketPath string) (*os.File, error) {
	lockPath := socketPath + ".lock"
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open daemon lock %s: %w", lockPath, err)
	}
	if err := lockFile.Chmod(0600); err != nil {
		lockFile.Close()
		return nil, fmt.Errorf("chmod daemon lock %s: %w", lockPath, err)
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lockFile.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, fmt.Errorf("another doubletake daemon is already running for %s", socketPath)
		}
		return nil, fmt.Errorf("lock daemon instance %s: %w", lockPath, err)
	}
	return lockFile, nil
}

func releaseInstanceLock(lockFile *os.File) {
	if lockFile == nil {
		return
	}
	_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
	_ = lockFile.Close()
}

func removeStaleSocket(socketPath string) error {
	info, err := os.Lstat(socketPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect control socket: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("control socket path %s exists and is not a Unix socket", socketPath)
	}
	conn, dialErr := net.DialTimeout("unix", socketPath, 250*time.Millisecond)
	if dialErr == nil {
		conn.Close()
		return fmt.Errorf("another doubletake daemon is already running for %s", socketPath)
	}
	if !errors.Is(dialErr, syscall.ECONNREFUSED) && !os.IsNotExist(dialErr) {
		return fmt.Errorf("probe existing control socket: %w", dialErr)
	}
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale socket: %w", err)
	}
	return nil
}
