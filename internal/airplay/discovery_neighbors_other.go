//go:build !windows

package airplay

import (
	"context"
	"net"
)

func knownNeighborTargets(_ []net.Interface) []string {
	return nil
}

func browseUnicastPreferredAirPlayDevices(_ context.Context, _ []net.Interface) ([]AirPlayDevice, error) {
	return nil, nil
}
