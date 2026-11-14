//go:build !windows

package airplay

// platformInterfaceIsWireless defers to the sysfs markers checked by
// networkInterfaceIsWireless on Linux.
func platformInterfaceIsWireless(string) bool { return false }
