//go:build windows

package airplay

import (
	"time"

	"golang.org/x/sys/windows"
)

var getTickCount64 = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetTickCount64")

// bootRelativeNow returns Windows' monotonic milliseconds since system start.
func bootRelativeNow() time.Duration {
	milliseconds, _, _ := getTickCount64.Call()
	return time.Duration(uint64(milliseconds)) * time.Millisecond
}
