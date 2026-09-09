//go:build windows

package airplay

import (
	"net"
	"sort"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	windowsErrorInsufficientBuffer = 122
	mibIPNetTypeDynamic            = 3
	mibIPNetTypeStatic             = 4
)

var getIPNetTable = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetIpNetTable")

type mibIPNetRow struct {
	interfaceIndex     uint32
	physicalAddressLen uint32
	physicalAddress    [8]byte
	address            uint32
	neighborType       uint32
}

func knownNeighborTargets(ifaces []net.Interface) []string {
	if len(ifaces) == 0 {
		return nil
	}

	var size uint32
	status, _, _ := getIPNetTable.Call(0, uintptr(unsafe.Pointer(&size)), 0)
	if status != windowsErrorInsufficientBuffer || size < 4 {
		return nil
	}

	table := make([]byte, size)
	status, _, _ = getIPNetTable.Call(
		uintptr(unsafe.Pointer(&table[0])),
		uintptr(unsafe.Pointer(&size)),
		0,
	)
	if status != 0 {
		return nil
	}

	interfaceIndexes := make(map[uint32]struct{}, len(ifaces))
	localAddresses := make(map[string]struct{})
	for _, iface := range ifaces {
		interfaceIndexes[uint32(iface.Index)] = struct{}{}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err == nil && ip.To4() != nil {
				localAddresses[ip.String()] = struct{}{}
			}
		}
	}

	return parseKnownNeighborTable(table[:size], interfaceIndexes, localAddresses)
}

func parseKnownNeighborTable(table []byte, interfaceIndexes map[uint32]struct{}, localAddresses map[string]struct{}) []string {
	if len(table) < 4 {
		return nil
	}

	rowSize := int(unsafe.Sizeof(mibIPNetRow{}))
	count := int(*(*uint32)(unsafe.Pointer(&table[0])))
	maxRows := (len(table) - 4) / rowSize
	if count > maxRows {
		count = maxRows
	}

	seen := make(map[string]struct{}, count)
	targets := make([]string, 0, count)
	for i := 0; i < count; i++ {
		offset := 4 + i*rowSize
		row := *(*mibIPNetRow)(unsafe.Pointer(&table[offset]))
		if !knownUnicastNeighbor(row, interfaceIndexes) {
			continue
		}

		address := *(*[4]byte)(unsafe.Pointer(&row.address))
		ip := net.IPv4(address[0], address[1], address[2], address[3])
		if !ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		ipString := ip.String()
		if _, local := localAddresses[ipString]; local {
			continue
		}
		if _, duplicate := seen[ipString]; duplicate {
			continue
		}
		seen[ipString] = struct{}{}
		targets = append(targets, ipString)
	}

	sort.Strings(targets)
	return targets
}

func knownUnicastNeighbor(row mibIPNetRow, interfaceIndexes map[uint32]struct{}) bool {
	if _, selected := interfaceIndexes[row.interfaceIndex]; !selected {
		return false
	}
	if row.neighborType != mibIPNetTypeDynamic && row.neighborType != mibIPNetTypeStatic {
		return false
	}
	if row.physicalAddressLen == 0 || row.physicalAddressLen > uint32(len(row.physicalAddress)) {
		return false
	}
	for _, b := range row.physicalAddress[:row.physicalAddressLen] {
		if b != 0 {
			return true
		}
	}
	return false
}
