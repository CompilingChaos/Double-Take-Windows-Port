//go:build windows

package airplay

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/miekg/dns"
	"golang.org/x/net/ipv4"
	"golang.org/x/sys/windows"
)

const (
	windowsErrorInsufficientBuffer = 122
	mibIPNetTypeDynamic            = 3
	mibIPNetTypeStatic             = 4
)

var getIPNetTable = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetIpNetTable")

var mdnsIPv4Address = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

type mibIPNetRow struct {
	interfaceIndex     uint32
	physicalAddressLen uint32
	physicalAddress    [8]byte
	address            uint32
	neighborType       uint32
}

func browseUnicastPreferredAirPlayDevices(ctx context.Context, ifaces []net.Interface) ([]AirPlayDevice, error) {
	if len(ifaces) == 0 {
		return nil, nil
	}

	conn, err := listenReusableMDNS(ctx)
	if err != nil {
		return nil, fmt.Errorf("listen on mDNS port: %w", err)
	}
	defer conn.Close()
	packetConn := ipv4.NewPacketConn(conn)

	var joined int
	for _, iface := range ifaces {
		if err := packetConn.JoinGroup(&iface, mdnsIPv4Address); err == nil {
			joined++
		}
	}
	if joined == 0 {
		return nil, fmt.Errorf("join mDNS group on selected interfaces")
	}

	packet, err := unicastPreferredAirPlayQuestion().Pack()
	if err != nil {
		return nil, fmt.Errorf("encode mDNS question: %w", err)
	}

	if err := sendMDNSQuestion(packetConn, packet, ifaces); err != nil {
		return nil, err
	}

	buffer := make([]byte, 65535)
	var devices []AirPlayDevice
	var responses int
	queriesSent := 1
	nextQuery := time.Now().Add(time.Second)
	for {
		select {
		case <-ctx.Done():
			devices = mergeAirPlayDevices(devices)
			dbg("[DISCOVERY] unicast-preferred mDNS received %d responses and found %d receivers", responses, len(devices))
			return devices, nil
		default:
		}
		if queriesSent < 3 && !time.Now().Before(nextQuery) {
			if err := sendMDNSQuestion(packetConn, packet, ifaces); err != nil {
				return devices, err
			}
			queriesSent++
			nextQuery = time.Now().Add(time.Second)
		}

		deadline := time.Now().Add(250 * time.Millisecond)
		if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
			deadline = contextDeadline
		}
		if err := conn.SetReadDeadline(deadline); err != nil {
			return devices, err
		}
		n, _, _, err := packetConn.ReadFrom(buffer)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			return devices, fmt.Errorf("read mDNS response: %w", err)
		}

		var response dns.Msg
		if err := response.Unpack(buffer[:n]); err != nil || !response.Response {
			continue
		}
		responses++
		devices = append(devices, airPlayDevicesFromDNSResponse(&response)...)
	}
}

func sendMDNSQuestion(packetConn *ipv4.PacketConn, packet []byte, ifaces []net.Interface) error {
	var sent bool
	var lastErr error
	for _, iface := range ifaces {
		if err := packetConn.SetMulticastInterface(&iface); err != nil {
			lastErr = err
			continue
		}
		if _, err := packetConn.WriteTo(packet, nil, mdnsIPv4Address); err != nil {
			lastErr = err
			continue
		}
		sent = true
	}
	if !sent && lastErr != nil {
		return fmt.Errorf("send mDNS question: %w", lastErr)
	}
	return nil
}

func listenReusableMDNS(ctx context.Context) (*net.UDPConn, error) {
	listenConfig := net.ListenConfig{
		Control: func(_, _ string, raw syscall.RawConn) error {
			var optionErr error
			if err := raw.Control(func(fd uintptr) {
				optionErr = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_REUSEADDR, 1)
			}); err != nil {
				return err
			}
			return optionErr
		},
	}
	packetConn, err := listenConfig.ListenPacket(ctx, "udp4", "0.0.0.0:5353")
	if err != nil {
		return nil, err
	}
	conn, ok := packetConn.(*net.UDPConn)
	if !ok {
		packetConn.Close()
		return nil, fmt.Errorf("mDNS listener is %T, want UDP", packetConn)
	}
	return conn, nil
}

func unicastPreferredAirPlayQuestion() *dns.Msg {
	return &dns.Msg{
		MsgHdr: dns.MsgHdr{RecursionDesired: false},
		Question: []dns.Question{{
			Name:   "_airplay._tcp.local.",
			Qtype:  dns.TypePTR,
			Qclass: dns.ClassINET | 1<<15,
		}},
	}
}

func airPlayDevicesFromDNSResponse(response *dns.Msg) []AirPlayDevice {
	records := make([]dns.RR, 0, len(response.Answer)+len(response.Ns)+len(response.Extra))
	records = append(records, response.Answer...)
	records = append(records, response.Ns...)
	records = append(records, response.Extra...)

	var instances []string
	servers := make(map[string]*dns.SRV)
	text := make(map[string][]string)
	addresses := make(map[string][]string)
	for _, record := range records {
		switch record := record.(type) {
		case *dns.PTR:
			if canonicalDNSName(record.Hdr.Name) == "_airplay._tcp.local." {
				instances = append(instances, record.Ptr)
			}
		case *dns.SRV:
			servers[canonicalDNSName(record.Hdr.Name)] = record
		case *dns.TXT:
			text[canonicalDNSName(record.Hdr.Name)] = record.Txt
		case *dns.A:
			host := canonicalDNSName(record.Hdr.Name)
			addresses[host] = append(addresses[host], record.A.String())
		case *dns.AAAA:
			host := canonicalDNSName(record.Hdr.Name)
			addresses[host] = append(addresses[host], record.AAAA.String())
		}
	}

	devices := make([]AirPlayDevice, 0, len(instances))
	for _, instance := range instances {
		serviceName := canonicalDNSName(instance)
		server := servers[serviceName]
		if server == nil {
			continue
		}
		hostAddresses := addresses[canonicalDNSName(server.Target)]
		if len(hostAddresses) == 0 {
			continue
		}

		device := AirPlayDevice{
			Name: airPlayInstanceName(instance),
			IP:   hostAddresses[0],
			Port: int(server.Port),
		}
		populateDeviceFromTXT(&device, parseTXT(text[serviceName]))
		devices = append(devices, device)
	}
	return devices
}

func canonicalDNSName(name string) string {
	return strings.ToLower(dns.Fqdn(name))
}

func airPlayInstanceName(name string) string {
	const suffix = "._airplay._tcp.local."
	name = dns.Fqdn(name)
	if len(name) >= len(suffix) && strings.EqualFold(name[len(name)-len(suffix):], suffix) {
		name = name[:len(name)-len(suffix)]
	}
	return unescapeDNSName(name)
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
