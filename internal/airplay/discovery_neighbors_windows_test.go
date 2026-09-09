//go:build windows

package airplay

import (
	"encoding/binary"
	"reflect"
	"testing"
	"unsafe"

	"github.com/miekg/dns"
)

func TestUnicastPreferredAirPlayQuestion(t *testing.T) {
	packet, err := unicastPreferredAirPlayQuestion().Pack()
	if err != nil {
		t.Fatal(err)
	}

	var decoded dns.Msg
	if err := decoded.Unpack(packet); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Question) != 1 {
		t.Fatalf("question count = %d, want 1", len(decoded.Question))
	}
	question := decoded.Question[0]
	if question.Name != "_airplay._tcp.local." || question.Qtype != dns.TypePTR {
		t.Fatalf("question = %+v, want AirPlay PTR", question)
	}
	if question.Qclass != dns.ClassINET|1<<15 {
		t.Fatalf("question class = %#x, want IN with unicast-response bit", question.Qclass)
	}
}

func TestAirPlayDevicesFromDNSResponse(t *testing.T) {
	const (
		service = "Room\\032Display._airplay._tcp.local."
		host    = "room-display.local."
	)
	response := &dns.Msg{
		MsgHdr: dns.MsgHdr{Response: true},
		Answer: []dns.RR{&dns.PTR{
			Hdr: dns.RR_Header{Name: "_airplay._tcp.local.", Rrtype: dns.TypePTR, Class: dns.ClassINET},
			Ptr: service,
		}},
		Extra: []dns.RR{
			&dns.SRV{
				Hdr:    dns.RR_Header{Name: service, Rrtype: dns.TypeSRV, Class: dns.ClassINET | 1<<15},
				Port:   7000,
				Target: host,
			},
			&dns.A{
				Hdr: dns.RR_Header{Name: host, Rrtype: dns.TypeA, Class: dns.ClassINET | 1<<15},
				A:   []byte{192, 168, 81, 233},
			},
			&dns.TXT{
				Hdr: dns.RR_Header{Name: service, Rrtype: dns.TypeTXT, Class: dns.ClassINET | 1<<15},
				Txt: []string{"model=AppleTV5,3", "deviceid=AA:BB:CC:DD:EE:FF", "features=0x1,0x2", "srcvers=960.13.1"},
			},
		},
	}

	got := airPlayDevicesFromDNSResponse(response)
	if len(got) != 1 {
		t.Fatalf("device count = %d, want 1", len(got))
	}
	device := got[0]
	if device.Name != "Room Display" || device.IP != "192.168.81.233" || device.Port != 7000 {
		t.Fatalf("device address fields = %+v", device)
	}
	if device.Model != "AppleTV5,3" || device.DeviceID != "AA:BB:CC:DD:EE:FF" || device.SourceVersion != "960.13.1" {
		t.Fatalf("device TXT fields = %+v", device)
	}
}

func TestParseKnownNeighborTableFiltersUnsafeCandidates(t *testing.T) {
	valid := mibIPNetRow{
		interfaceIndex:     21,
		physicalAddressLen: 6,
		physicalAddress:    [8]byte{0xba, 0xa2, 0xe7, 0xae, 0xbe, 0xa3},
		address:            ipv4TableAddress(192, 168, 82, 23),
		neighborType:       mibIPNetTypeDynamic,
	}
	rows := []mibIPNetRow{
		valid,
		valid,
		{interfaceIndex: 22, physicalAddressLen: 6, physicalAddress: [8]byte{1}, address: ipv4TableAddress(10, 0, 0, 2), neighborType: mibIPNetTypeDynamic},
		{interfaceIndex: 21, physicalAddressLen: 6, physicalAddress: [8]byte{1}, address: ipv4TableAddress(10, 0, 0, 3), neighborType: 2},
		{interfaceIndex: 21, physicalAddressLen: 6, address: ipv4TableAddress(10, 0, 0, 4), neighborType: mibIPNetTypeDynamic},
		{interfaceIndex: 21, physicalAddressLen: 6, physicalAddress: [8]byte{1}, address: ipv4TableAddress(224, 0, 0, 251), neighborType: mibIPNetTypeStatic},
		{interfaceIndex: 21, physicalAddressLen: 6, physicalAddress: [8]byte{1}, address: ipv4TableAddress(192, 168, 83, 58), neighborType: mibIPNetTypeDynamic},
	}

	table := encodeNeighborTable(rows)
	got := parseKnownNeighborTable(
		table,
		map[uint32]struct{}{21: {}},
		map[string]struct{}{"192.168.83.58": {}},
	)
	want := []string{"192.168.82.23"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseKnownNeighborTable() = %v, want %v", got, want)
	}
}

func TestParseKnownNeighborTableClampsTruncatedRowCount(t *testing.T) {
	table := encodeNeighborTable([]mibIPNetRow{{
		interfaceIndex:     21,
		physicalAddressLen: 6,
		physicalAddress:    [8]byte{1},
		address:            ipv4TableAddress(10, 0, 0, 2),
		neighborType:       mibIPNetTypeDynamic,
	}})
	binary.LittleEndian.PutUint32(table[:4], 1000)

	got := parseKnownNeighborTable(table, map[uint32]struct{}{21: {}}, nil)
	want := []string{"10.0.0.2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseKnownNeighborTable() = %v, want %v", got, want)
	}
}

func encodeNeighborTable(rows []mibIPNetRow) []byte {
	rowSize := int(unsafe.Sizeof(mibIPNetRow{}))
	table := make([]byte, 4+len(rows)*rowSize)
	binary.LittleEndian.PutUint32(table[:4], uint32(len(rows)))
	for i := range rows {
		rowBytes := unsafe.Slice((*byte)(unsafe.Pointer(&rows[i])), rowSize)
		copy(table[4+i*rowSize:], rowBytes)
	}
	return table
}

func ipv4TableAddress(a, b, c, d byte) uint32 {
	return binary.LittleEndian.Uint32([]byte{a, b, c, d})
}
