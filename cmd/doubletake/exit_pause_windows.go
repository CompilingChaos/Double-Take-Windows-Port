//go:build windows

package main

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

var getConsoleProcessList = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleProcessList")

func shouldPauseOnExit() bool {
	var processIDs [2]uint32
	count, _, _ := getConsoleProcessList.Call(
		uintptr(unsafe.Pointer(&processIDs[0])),
		uintptr(len(processIDs)),
	)
	return count == 1
}

func waitForExitKey() {
	fmt.Fprint(os.Stderr, "\nPress any key to close...")

	console := windows.Handle(os.Stdin.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(console, &mode); err != nil {
		return
	}

	rawMode := mode &^ (windows.ENABLE_LINE_INPUT | windows.ENABLE_ECHO_INPUT)
	if err := windows.SetConsoleMode(console, rawMode); err != nil {
		return
	}
	defer windows.SetConsoleMode(console, mode)

	var key [1]byte
	var read uint32
	_ = windows.ReadFile(console, key[:], &read, nil)
	fmt.Fprintln(os.Stderr)
}
