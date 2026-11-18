// Package netdns switches this computer's DNS resolvers to Cloudflare
// (1.1.1.1, using DNS over HTTPS where Windows supports it) and back.
//
// The change applies to every connected network adapter that has a default
// gateway. It is a system setting: it needs administrator approval, and it
// stays in effect after DoubleTake exits until it is switched off again.
// AirPlay discovery and mirroring use multicast DNS and receiver addresses,
// so they do not depend on this setting.
package netdns

import (
	"errors"
	"net"
	"strings"
)

// State is the DNS configuration of the connected network adapters.
type State int

const (
	// StateUnknown means the configuration has not been read yet.
	StateUnknown State = iota
	// StateOff means at least one connected adapter does not use Cloudflare.
	StateOff
	// StateOn means every connected adapter uses Cloudflare.
	StateOn
	// StateUnavailable means the setting cannot be read or changed here.
	StateUnavailable
)

// Cloudflare's public resolvers, primary first.
var (
	CloudflareIPv4 = []string{"1.1.1.1", "1.0.0.1"}
	CloudflareIPv6 = []string{"2606:4700:4700::1111", "2606:4700:4700::1001"}
)

var (
	// ErrUnsupported is returned on platforms without a DNS switch.
	ErrUnsupported = errors.New("switching DNS servers is not supported on this platform")
	// ErrDeclined is returned when the administrator prompt is cancelled.
	ErrDeclined = errors.New("administrator permission was declined")
	// ErrNoAdapter is returned when no adapter is connected to a network.
	ErrNoAdapter = errors.New("no connected network adapter was found")
	// ErrNotApplied is returned when the change did not take effect.
	ErrNotApplied = errors.New("the DNS change did not take effect")
)

func isCloudflare(address string) bool {
	for _, server := range append(append([]string(nil), CloudflareIPv4...), CloudflareIPv6...) {
		if strings.EqualFold(server, address) {
			return true
		}
	}
	return false
}

// usesCloudflare reports whether a resolver list puts Cloudflare first, so
// lookups go to Cloudflare rather than to a fallback.
func usesCloudflare(servers []string) bool {
	for _, server := range servers {
		if ip := net.ParseIP(server); ip != nil && ip.To4() != nil {
			return isCloudflare(server)
		}
	}
	return false
}
