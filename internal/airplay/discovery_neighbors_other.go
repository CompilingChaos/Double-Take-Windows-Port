//go:build !windows

package airplay

import "net"

func knownNeighborTargets(_ []net.Interface) []string {
	return nil
}
