package airplay

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/grandcat/zeroconf"
	"howett.net/plist"
)

// AirPlayDevice represents a discovered AirPlay receiver.
type AirPlayDevice struct {
	Name     string
	Model    string
	IP       string
	Port     int
	DeviceID string
	Features uint64
	PK       string // hex-encoded Ed25519 public key
	Flags    uint64
}

// DiscoverAirPlayDevices browses the local network for AirPlay receivers.
func DiscoverAirPlayDevices(ctx context.Context) ([]AirPlayDevice, error) {
	if runtime.GOOS == "windows" {
		return discoverAirPlayDevicesWindows(ctx)
	}
	return browseAirPlayDevices(ctx)
}

func discoverAirPlayDevicesWindows(ctx context.Context) ([]AirPlayDevice, error) {
	type discoveryResult struct {
		devices []AirPlayDevice
		err     error
	}

	mdnsResults := make(chan discoveryResult, 1)
	scanResults := make(chan discoveryResult, 1)
	mdnsCtx, cancelMDNS := context.WithTimeout(ctx, 3*time.Second)
	defer cancelMDNS()

	go func() {
		devices, err := browseAirPlayDevices(mdnsCtx)
		mdnsResults <- discoveryResult{devices: devices, err: err}
	}()
	go func() {
		devices, err := scanLocalAirPlayDevices(ctx)
		scanResults <- discoveryResult{devices: devices, err: err}
	}()

	mdns := <-mdnsResults
	scan := <-scanResults
	devices := mergeAirPlayDevices(mdns.devices, scan.devices)
	if len(devices) > 0 {
		return preferAppleTVDevices(devices), nil
	}
	if scan.err != nil {
		return nil, scan.err
	}
	if mdns.err != nil {
		return nil, mdns.err
	}
	return nil, nil
}

func browseAirPlayDevices(ctx context.Context) ([]AirPlayDevice, error) {
	var opts []zeroconf.ClientOption
	if ifaces := preferredDiscoveryInterfaces(); len(ifaces) > 0 {
		opts = append(opts, zeroconf.SelectIfaces(ifaces))
		dbg("[DISCOVERY] using interfaces: %s", interfaceNames(ifaces))
	}

	resolver, err := zeroconf.NewResolver(opts...)
	if err != nil {
		return nil, fmt.Errorf("zeroconf resolver: %w", err)
	}

	entries := make(chan *zeroconf.ServiceEntry, 16)
	var devices []AirPlayDevice

	done := make(chan struct{})
	go func() {
		defer close(done)
		for entry := range entries {
			dev := parseServiceEntry(entry)
			if dev != nil {
				devices = append(devices, *dev)
			}
		}
	}()

	if err := resolver.Browse(ctx, "_airplay._tcp", "local.", entries); err != nil {
		return nil, fmt.Errorf("browse: %w", err)
	}

	<-ctx.Done()
	<-done
	return devices, nil
}

func scanLocalAirPlayDevices(ctx context.Context) ([]AirPlayDevice, error) {
	ifaces := preferredDiscoveryInterfaces()
	if len(ifaces) == 0 {
		return nil, nil
	}

	var targets []string
	for _, iface := range ifaces {
		targets = append(targets, scanTargetsForInterface(iface)...)
	}
	targets = uniqueStrings(targets)
	rand.Shuffle(len(targets), func(i, j int) {
		targets[i], targets[j] = targets[j], targets[i]
	})
	if len(targets) == 0 {
		return nil, nil
	}
	dbg("[DISCOVERY] probing %d hosts on local subnet", len(targets))

	targetCh := make(chan string)
	resultCh := make(chan AirPlayDevice, 16)
	var wg sync.WaitGroup
	workers := 192
	if len(targets) < workers {
		workers = len(targets)
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ip := range targetCh {
				if ctx.Err() != nil {
					return
				}
				if dev, ok := probeAirPlayInfo(ctx, ip, 7000); ok {
					select {
					case resultCh <- dev:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
	}

	go func() {
		defer close(targetCh)
		for _, ip := range targets {
			select {
			case targetCh <- ip:
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	var devices []AirPlayDevice
	for dev := range resultCh {
		devices = append(devices, dev)
	}
	return devices, nil
}

func scanTargetsForInterface(iface net.Interface) []string {
	addrs, err := iface.Addrs()
	if err != nil {
		return nil
	}

	var targets []string
	for _, addr := range addrs {
		ip, ipNet, err := net.ParseCIDR(addr.String())
		if err != nil {
			continue
		}
		ip4 := ip.To4()
		if ip4 == nil || ip4.IsLoopback() || ip4.IsLinkLocalUnicast() {
			continue
		}

		ones, bits := ipNet.Mask.Size()
		if bits != 32 || ones < 20 {
			// Avoid spraying enormous networks. The known campus-style /21 and
			// normal home /24 networks are both small enough to probe quickly.
			continue
		}

		start := binary.BigEndian.Uint32(ip4) & binary.BigEndian.Uint32([]byte(ipNet.Mask))
		size := uint32(1) << uint32(32-ones)
		for i := uint32(1); i+1 < size; i++ {
			candidate := make(net.IP, 4)
			binary.BigEndian.PutUint32(candidate, start+i)
			if candidate.Equal(ip4) {
				continue
			}
			targets = append(targets, candidate.String())
		}
	}
	return targets
}

func probeAirPlayInfo(ctx context.Context, ip string, port int) (AirPlayDevice, bool) {
	var conn net.Conn
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		dialer := net.Dialer{Timeout: 225 * time.Millisecond}
		conn, err = dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip, strconv.Itoa(port)))
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return AirPlayDevice{}, false
		}
	}
	if err != nil {
		return AirPlayDevice{}, false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(900 * time.Millisecond))

	req := "GET /info RTSP/1.0\r\n" +
		"CSeq: 1\r\n" +
		"User-Agent: AirPlay/935.7.1\r\n" +
		"Content-Length: 0\r\n\r\n"
	if _, err := io.WriteString(conn, req); err != nil {
		return AirPlayDevice{}, false
	}

	body, err := readRTSPBody(conn)
	if err != nil || len(body) == 0 {
		return AirPlayDevice{}, false
	}

	var info ReceiverInfo
	if _, err := plist.Unmarshal(body, &info); err != nil {
		return AirPlayDevice{}, false
	}
	if info.Model == "" && info.Name == "" {
		return AirPlayDevice{}, false
	}
	if !looksLikeAppleTVReceiver(info) {
		return AirPlayDevice{}, false
	}

	name := info.Name
	if name == "" {
		name = ip
	}
	dbg("[DISCOVERY] found AirPlay receiver by subnet scan: %s %s:%d (%s)", name, ip, port, info.Model)
	return AirPlayDevice{
		Name:     name,
		Model:    info.Model,
		IP:       ip,
		Port:     port,
		DeviceID: info.DeviceID,
		Features: info.Features,
		PK:       fmt.Sprintf("%x", info.PK),
	}, true
}

func readRTSPBody(conn net.Conn) ([]byte, error) {
	reader := bufio.NewReader(conn)
	status, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if !strings.Contains(status, " 200 ") {
		return nil, fmt.Errorf("unexpected RTSP status: %s", strings.TrimSpace(status))
	}

	contentLength := 0
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(k), "Content-Length") {
			contentLength, _ = strconv.Atoi(strings.TrimSpace(v))
		}
	}
	if contentLength <= 0 {
		return nil, nil
	}

	body := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, err
	}
	return body, nil
}

func looksLikeAppleTVReceiver(info ReceiverInfo) bool {
	text := strings.ToLower(info.Model + " " + info.Manufacturer + " " + info.Name)
	if strings.Contains(text, "appletv") || strings.Contains(text, "apple tv") {
		return true
	}
	return false
}

func preferAppleTVDevices(devices []AirPlayDevice) []AirPlayDevice {
	var appleTVs []AirPlayDevice
	for _, dev := range devices {
		if strings.Contains(strings.ToLower(dev.Model), "appletv") {
			appleTVs = append(appleTVs, dev)
		}
	}
	if len(appleTVs) > 0 {
		return appleTVs
	}
	return devices
}

func mergeAirPlayDevices(groups ...[]AirPlayDevice) []AirPlayDevice {
	byAddress := make(map[string]AirPlayDevice)
	order := make([]string, 0)
	for _, devices := range groups {
		for _, dev := range devices {
			key := net.JoinHostPort(dev.IP, strconv.Itoa(dev.Port))
			if existing, ok := byAddress[key]; ok {
				byAddress[key] = richerAirPlayDevice(existing, dev)
				continue
			}
			byAddress[key] = dev
			order = append(order, key)
		}
	}

	merged := make([]AirPlayDevice, 0, len(order))
	for _, key := range order {
		merged = append(merged, byAddress[key])
	}
	return merged
}

func richerAirPlayDevice(a, b AirPlayDevice) AirPlayDevice {
	if a.Name == "" {
		a.Name = b.Name
	}
	if a.Model == "" {
		a.Model = b.Model
	}
	if a.DeviceID == "" {
		a.DeviceID = b.DeviceID
	}
	if a.PK == "" {
		a.PK = b.PK
	}
	if a.Features == 0 {
		a.Features = b.Features
	}
	if a.Flags == 0 {
		a.Flags = b.Flags
	}
	return a
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func preferredDiscoveryInterfaces() []net.Interface {
	if runtime.GOOS != "windows" {
		return nil
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	includeVirtual := os.Getenv("DOUBLETAKE_DISCOVERY_ALL_INTERFACES") != ""
	var preferred []net.Interface
	seen := make(map[int]struct{})
	if defaultIface := defaultRouteInterface(ifaces); defaultIface != nil && usableDiscoveryInterface(*defaultIface, includeVirtual) {
		preferred = append(preferred, *defaultIface)
		seen[defaultIface.Index] = struct{}{}
	}

	for _, iface := range ifaces {
		if _, ok := seen[iface.Index]; ok || !usableDiscoveryInterface(iface, includeVirtual) {
			continue
		}
		preferred = append(preferred, iface)
	}
	return preferred
}

func usableDiscoveryInterface(iface net.Interface, includeVirtual bool) bool {
	if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 || iface.Flags&net.FlagLoopback != 0 {
		return false
	}

	if !includeVirtual {
		name := strings.ToLower(iface.Name)
		for _, blocked := range []string{
			"tailscale",
			"vethernet",
			"virtualbox",
			"vmware",
			"hyper-v",
			"loopback",
			"wireguard",
			"wintun",
			"openvpn",
			"wsl",
			"docker",
			"tap-windows",
		} {
			if strings.Contains(name, blocked) {
				return false
			}
		}
	}

	addrs, err := iface.Addrs()
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		ip, _, err := net.ParseCIDR(addr.String())
		if err != nil || ip == nil {
			continue
		}
		if ip4 := ip.To4(); ip4 != nil && !ip4.IsLoopback() && !ip4.IsLinkLocalUnicast() {
			return true
		}
	}
	return false
}

func defaultRouteInterface(ifaces []net.Interface) *net.Interface {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return nil
	}
	defer conn.Close()

	udpAddr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || udpAddr.IP == nil {
		return nil
	}
	localIP := udpAddr.IP.To4()
	if localIP == nil {
		return nil
	}

	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err != nil || ip == nil {
				continue
			}
			if ip.To4() != nil && ip.Equal(localIP) {
				return &iface
			}
		}
	}
	return nil
}

func interfaceNames(ifaces []net.Interface) string {
	names := make([]string, 0, len(ifaces))
	for _, iface := range ifaces {
		names = append(names, iface.Name)
	}
	return strings.Join(names, ", ")
}

func parseServiceEntry(entry *zeroconf.ServiceEntry) *AirPlayDevice {
	if len(entry.AddrIPv4) == 0 && len(entry.AddrIPv6) == 0 {
		return nil
	}

	dev := &AirPlayDevice{
		Name: unescapeDNSName(entry.Instance),
		Port: entry.Port,
	}

	if len(entry.AddrIPv4) > 0 {
		dev.IP = entry.AddrIPv4[0].String()
	} else if len(entry.AddrIPv6) > 0 {
		dev.IP = entry.AddrIPv6[0].String()
	}

	txt := parseTXT(entry.Text)
	dev.Model = txt["model"]
	dev.DeviceID = txt["deviceid"]
	dev.PK = txt["pk"]

	if f := txt["features"]; f != "" {
		dev.Features = parseFeatures(f)
	}
	if f := txt["flags"]; f != "" {
		fmt.Sscanf(f, "0x%x", &dev.Flags)
	}

	return dev
}

func parseTXT(records []string) map[string]string {
	m := make(map[string]string, len(records))
	for _, r := range records {
		k, v, _ := strings.Cut(r, "=")
		m[k] = v
	}
	return m
}

// unescapeDNSName removes DNS-SD backslash escapes from an mDNS instance name.
// e.g. "Living\ Room\ \(2\)" -> "Living Room (2)"
func unescapeDNSName(s string) string {
	buf := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			if i+3 < len(s) && isASCIIDigit(s[i+1]) && isASCIIDigit(s[i+2]) && isASCIIDigit(s[i+3]) {
				v, err := strconv.Atoi(s[i+1 : i+4])
				if err == nil && v >= 0 && v <= 255 {
					buf = append(buf, byte(v))
					i += 3
					continue
				}
			}

			i++
		} else {
			buf = append(buf, s[i])
			continue
		}

		buf = append(buf, s[i])
	}
	return string(buf)
}

func isASCIIDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

// parseFeatures parses the AirPlay features string "0xHIGH,0xLOW" into a 64-bit value.
func parseFeatures(s string) uint64 {
	parts := strings.Split(s, ",")
	if len(parts) != 2 {
		var v uint64
		fmt.Sscanf(s, "0x%x", &v)
		return v
	}
	var lo, hi uint64
	fmt.Sscanf(parts[0], "0x%x", &lo)
	fmt.Sscanf(parts[1], "0x%x", &hi)
	return hi<<32 | lo
}

// Feature bit constants for AirPlay receivers.
const (
	FeatureScreen           uint64 = 1 << 8
	FeatureAudio            uint64 = 1 << 10
	FeatureFPSAP25          uint64 = 1 << 14
	FeatureHomeKitPairing   uint64 = 1 << 17
	FeatureTransientPairing uint64 = 1 << 19
	FeatureUDPMirroring     uint64 = 1 << 49
)

func (d *AirPlayDevice) SupportsScreen() bool {
	return d.Features&FeatureScreen != 0
}

func (d *AirPlayDevice) SupportsTransientPairing() bool {
	return d.Features&FeatureTransientPairing != 0
}

func (d *AirPlayDevice) SupportsFairPlaySAP() bool {
	return d.Features&FeatureFPSAP25 != 0
}

func (i *ReceiverInfo) SupportsFairPlaySAP() bool {
	return i != nil && i.Features&FeatureFPSAP25 != 0
}

// playoutLatencyFloor returns the minimum playout lead this receiver needs.
// Modern Apple receivers advertise FairPlay SAP and have robust audio jitter
// buffers, so they can play at very low latency (floor 0). Receivers without it
// (Roku and other third-party AirPlay implementations) need a conservative lead
// or they drop audio they can no longer schedule.
func (i *ReceiverInfo) playoutLatencyFloor() time.Duration {
	if i != nil && i.SupportsFairPlaySAP() {
		return 0
	}
	return conservativePlayoutLatency
}
