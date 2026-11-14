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

func TestDiscoveryListsOneAddressPerReceiver(t *testing.T) {
	v4 := airplay.AirPlayDevice{Name: "TV", IP: "192.0.2.20", Port: 7000, DeviceID: "AA:BB:CC:DD:EE:FF"}
	v6 := airplay.AirPlayDevice{Name: "TV", IP: "fe80::20", Port: 7000, DeviceID: "aa:bb:cc:dd:ee:ff"}
	v6Only := airplay.AirPlayDevice{Name: "TV", IP: "2001:db8::30", Port: 7000, DeviceID: "other"}
	unknown := airplay.AirPlayDevice{Name: "TV", IP: "192.0.2.40", Port: 7000}
	d := &Daemon{devices: []airplay.AirPlayDevice{v6, v6Only, unknown, v4}}

	for _, command := range []string{"devices", "discover"} {
		got := d.Execute(Request{Cmd: command}).Devices
		if len(got) != 3 || got[0].IP != v4.IP || got[1].IP != v6Only.IP || got[2].IP != unknown.IP {
			t.Fatalf("%s returned %+v, want preferred IPv4, IPv6-only, and unknown receivers", command, got)
		}
	}

	d.devices = d.devices[:3]
	got := d.Execute(Request{Cmd: "devices"}).Devices
	if len(got) != 3 || got[0].IP != v6.IP {
		t.Fatalf("discovery after IPv4 loss returned %+v, want the remaining IPv6 address", got)
	}
}

func TestActiveReceiverKeepsItsConnectedAddress(t *testing.T) {
	v4 := airplay.AirPlayDevice{Name: "TV", IP: "192.0.2.20", Port: 7000, DeviceID: "receiver"}
	v6 := airplay.AirPlayDevice{Name: "TV", IP: "2001:db8::30", Port: 7000, DeviceID: "RECEIVER"}
	d := &Daemon{
		devices: []airplay.AirPlayDevice{v4, v6},
		streams: map[string]*activeStream{
			v6.IP: {deviceIP: v6.IP, deviceID: v6.DeviceID, state: StateStreaming},
		},
	}

	got := d.Execute(Request{Cmd: "devices"}).Devices
	if len(got) != 1 || got[0].IP != v6.IP {
		t.Fatalf("discovery returned %+v, want the active IPv6 endpoint", got)
	}
	if target, _ := d.pickFreeDeviceLocked(0); target != "" {
		t.Fatalf("automatic connect selected an alias of an active receiver: %s", target)
	}
	if response := d.Execute(Request{Cmd: "connect", Target: v4.IP}); response.OK || response.State != StateStreaming {
		t.Fatalf("connect via an alias returned %+v, want active receiver rejection", response)
	}
	d.devices = d.devices[:1]
	gotStatus := d.Execute(Request{Cmd: "status"}).Streams
	if len(gotStatus) != 1 || gotStatus[0].DeviceID != v6.DeviceID {
		t.Fatalf("stream status returned %+v, want the receiver ID for alternate address matching", gotStatus)
	}
}
