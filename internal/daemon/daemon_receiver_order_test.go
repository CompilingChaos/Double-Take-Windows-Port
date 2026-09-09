package daemon

import (
	"testing"

	"doubletake/internal/airplay"
)

func TestPickFreeDevicePrefersAppleTV(t *testing.T) {
	d := &Daemon{
		devices: []airplay.AirPlayDevice{
			{Name: "MacBook", Model: "MacBookAir10,1", IP: "192.168.1.10", Port: 7000},
			{Name: "Living Room", Model: "AppleTV14,1", IP: "192.168.1.20", Port: 7000},
		},
		streams: make(map[string]*activeStream),
	}

	target, port := d.pickFreeDeviceLocked(0)
	if target != "192.168.1.20" || port != 7000 {
		t.Fatalf("pickFreeDeviceLocked() = %s:%d, want Apple TV 192.168.1.20:7000", target, port)
	}
}

func TestPickFreeDeviceFallsBackToMac(t *testing.T) {
	d := &Daemon{
		devices: []airplay.AirPlayDevice{
			{Name: "Living Room", Model: "AppleTV14,1", IP: "192.168.1.20", Port: 7000},
			{Name: "MacBook", Model: "MacBookAir10,1", IP: "192.168.1.10", Port: 7100},
		},
		streams: map[string]*activeStream{
			"192.168.1.20": {deviceIP: "192.168.1.20"},
		},
	}

	target, port := d.pickFreeDeviceLocked(0)
	if target != "192.168.1.10" || port != 7100 {
		t.Fatalf("pickFreeDeviceLocked() = %s:%d, want MacBook 192.168.1.10:7100", target, port)
	}
}
