package daemon

import (
	"net/netip"
	"strings"

	"doubletake/internal/airplay"
)

// Call with d.mu held.
func (d *Daemon) activeReceiverLocked(ip, deviceID string) *activeStream {
	if stream := d.streams[ip]; stream != nil {
		return stream
	}
	if deviceID != "" {
		for _, stream := range d.streams {
			if strings.EqualFold(stream.deviceID, deviceID) {
				return stream
			}
		}
	}
	return nil
}

// preferredReceiversLocked selects one address per advertised receiver ID.
// Keep all addresses in discovery so an IPv6 address can still be used when it
// is the only one available. A receiver with no ID is kept as its own entry.
func (d *Daemon) preferredReceiversLocked() []airplay.AirPlayDevice {
	devices := make([]airplay.AirPlayDevice, 0, len(d.devices))
	byID := make(map[string]int, len(d.devices))
	for _, device := range d.devices {
		id := strings.ToLower(device.DeviceID)
		if idx, ok := byID[id]; ok {
			if d.receiverAddressPriority(device.IP) > d.receiverAddressPriority(devices[idx].IP) {
				devices[idx] = device
			}
			continue
		}
		if id != "" {
			byID[id] = len(devices)
		}
		devices = append(devices, device)
	}
	return devices
}

func (d *Daemon) receiverAddressPriority(ip string) int {
	if d.streams[ip] != nil {
		return 3
	}
	if addr, err := netip.ParseAddr(ip); err == nil {
		if addr.Unmap().Is4() {
			return 2
		}
		if !addr.IsLinkLocalUnicast() || addr.Zone() != "" {
			return 1
		}
	}
	return 0
}
