//go:build !windows

package main

func shouldPauseOnExit() bool {
	return false
}

func waitForExitKey() {}
