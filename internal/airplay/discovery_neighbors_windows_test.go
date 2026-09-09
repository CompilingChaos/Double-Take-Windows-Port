//go:build windows

package airplay

import (
	"encoding/binary"
	"reflect"
	"testing"
	"unsafe"
)

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
