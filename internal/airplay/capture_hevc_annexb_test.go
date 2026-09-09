package airplay

import (
	"bytes"
	"io"
	"testing"
)

func hevcTestNAL(typ byte, payload ...byte) []byte {
	nal := append([]byte{0, 0, 0, 1, typ << 1, 1}, payload...)
	return nal
}

func TestAnnexBHEVCReaderGroupsAUDDelimitedPictures(t *testing.T) {
	stream := bytes.Join([][]byte{
		hevcTestNAL(35, 0x50),
		hevcTestNAL(32, 1),
		hevcTestNAL(33, 2),
		hevcTestNAL(34, 3),
		hevcTestNAL(19, 0x80, 4),
		hevcTestNAL(35, 0x50),
		hevcTestNAL(1, 0x80, 5),
	}, nil)
	reader := newAnnexBHEVCAccessUnitReader(bytes.NewReader(stream))

	first, err := reader.ReadVideoAccessUnit()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(splitAnnexBAccessUnit(first.AnnexB)); got != 5 {
		t.Fatalf("first access unit has %d NALs, want 5", got)
	}
	second, err := reader.ReadVideoAccessUnit()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(splitAnnexBAccessUnit(second.AnnexB)); got != 2 {
		t.Fatalf("second access unit has %d NALs, want 2", got)
	}
	if _, err := reader.ReadVideoAccessUnit(); err != io.EOF {
		t.Fatalf("final read = %v, want EOF", err)
	}
}

func TestAnnexBHEVCReaderUsesFirstSliceWithoutAUD(t *testing.T) {
	stream := bytes.Join([][]byte{
		hevcTestNAL(1, 0x80, 1),
		hevcTestNAL(1, 0x00, 2),
		hevcTestNAL(1, 0x80, 3),
	}, nil)
	reader := newAnnexBHEVCAccessUnitReader(bytes.NewReader(stream))

	first, err := reader.ReadVideoAccessUnit()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(splitAnnexBAccessUnit(first.AnnexB)); got != 2 {
		t.Fatalf("first access unit has %d slices, want 2", got)
	}
	second, err := reader.ReadVideoAccessUnit()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(splitAnnexBAccessUnit(second.AnnexB)); got != 1 {
		t.Fatalf("second access unit has %d slices, want 1", got)
	}
}
