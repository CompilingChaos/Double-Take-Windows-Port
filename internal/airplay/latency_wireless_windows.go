//go:build windows

package airplay

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// ifTypeIEEE80211 is IF_TYPE_IEEE80211, the interface type of Wi-Fi adapters.
const ifTypeIEEE80211 = 71

// platformInterfaceIsWireless reports whether the named Windows adapter is a
// Wi-Fi adapter. Go names Windows interfaces by their adapter friendly name.
func platformInterfaceIsWireless(name string) bool {
	if name == "" {
		return false
	}
	size := uint32(15 * 1024)
	for attempt := 0; attempt < 3; attempt++ {
		buffer := make([]byte, size)
		first := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buffer[0]))
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, windows.GAA_FLAG_SKIP_ANYCAST|
			windows.GAA_FLAG_SKIP_MULTICAST|windows.GAA_FLAG_SKIP_DNS_SERVER, 0, first, &size)
		if err == windows.ERROR_BUFFER_OVERFLOW {
			continue
		}
		if err != nil {
			return false
		}
		for adapter := first; adapter != nil; adapter = adapter.Next {
			if windows.UTF16PtrToString(adapter.FriendlyName) == name {
				return adapter.IfType == ifTypeIEEE80211
			}
		}
		return false
	}
	return false
}
