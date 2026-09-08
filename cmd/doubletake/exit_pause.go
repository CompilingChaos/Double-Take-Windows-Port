package main

import (
	"log"
	"os"
	"sync"
)

var (
	pauseAtExit = shouldPauseOnExit()
	pauseOnce   sync.Once
)

func pauseForStandaloneConsole() {
	if pauseAtExit {
		pauseOnce.Do(waitForExitKey)
	}
}

func exitProgram(code int) {
	pauseForStandaloneConsole()
	os.Exit(code)
}

func fatalf(format string, args ...any) {
	log.Printf(format, args...)
	exitProgram(1)
}
