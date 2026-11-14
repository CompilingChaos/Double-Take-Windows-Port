//go:build windows

package airplay

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	getTickCount64 = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetTickCount64")
	// QueryInterruptTimePrecise counts the same time since system start as
	// GetTickCount64, including sleep, but with 100 ns precision instead of the
	// 15.6 ms timer tick. That tick would otherwise quantize every NTP timing
	// reply and video timestamp sent to NTP receivers such as Macs.
	queryInterruptTimePrecise = windows.NewLazySystemDLL("KernelBase.dll").NewProc("QueryInterruptTimePrecise")
	interruptTimeAvailable    = queryInterruptTimePrecise.Find() == nil
)

// bootRelativeNow returns Windows' monotonic time since system start.
func bootRelativeNow() time.Duration {
	if interruptTimeAvailable {
		var interruptTime uint64
		queryInterruptTimePrecise.Call(uintptr(unsafe.Pointer(&interruptTime)))
		return time.Duration(interruptTime) * 100
	}
	milliseconds, _, _ := getTickCount64.Call()
	return time.Duration(uint64(milliseconds)) * time.Millisecond
}
