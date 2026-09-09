package airplay

import (
	"fmt"
	"io"
)

// annexBHEVCAccessUnitReader groups an elementary HEVC byte stream into
// pictures. FFmpeg is configured to emit AUD NALs, while first-slice detection
// keeps the boundary correct for encoders that omit them.
type annexBHEVCAccessUnitReader struct {
	reader  io.Reader
	parser  *h264Parser
	readBuf []byte
	nals    [][]byte
	pending []byte
	eof     bool

	accessUnit []byte
	haveVCL    bool
}

func newAnnexBHEVCAccessUnitReader(reader io.Reader) videoAccessUnitReader {
	return &annexBHEVCAccessUnitReader{
		reader:  reader,
		parser:  newH264Parser(),
		readBuf: make([]byte, 64*1024),
	}
}

func (r *annexBHEVCAccessUnitReader) ReadVideoAccessUnit() (VideoAccessUnit, error) {
	for {
		nal, err := r.nextNAL()
		if err != nil {
			if err == io.EOF && r.haveVCL {
				return r.finishAccessUnit(), nil
			}
			return VideoAccessUnit{}, err
		}
		raw := stripStartCode(nal)
		if len(raw) < 2 {
			continue
		}
		typ := hevcNALType(raw)
		firstSlice := typ <= 31 && len(raw) > 2 && raw[2]&0x80 != 0
		prefixAfterPicture := r.haveVCL && (typ == 32 || typ == 33 || typ == 34 || typ == 35 || typ == 39)
		if r.haveVCL && (firstSlice || prefixAfterPicture) {
			r.pending = nal
			return r.finishAccessUnit(), nil
		}
		if len(r.accessUnit)+annexBLongStartCodeLength+len(raw) > maxVideoAccessUnitBytes {
			return VideoAccessUnit{}, fmt.Errorf("HEVC access unit exceeds %d bytes", maxVideoAccessUnitBytes)
		}
		r.accessUnit = append(r.accessUnit, annexBLongStartCode[:]...)
		r.accessUnit = append(r.accessUnit, raw...)
		if typ <= 31 {
			r.haveVCL = true
		}
	}
}

func (r *annexBHEVCAccessUnitReader) finishAccessUnit() VideoAccessUnit {
	unit := VideoAccessUnit{AnnexB: r.accessUnit}
	r.accessUnit = nil
	r.haveVCL = false
	return unit
}

func (r *annexBHEVCAccessUnitReader) nextNAL() ([]byte, error) {
	if len(r.pending) != 0 {
		nal := r.pending
		r.pending = nil
		return nal, nil
	}
	for {
		if len(r.nals) != 0 {
			nal := r.nals[0]
			r.nals = r.nals[1:]
			return nal, nil
		}
		if r.eof {
			if start := findStartCode(r.parser.buf, 0); start >= 0 && start < len(r.parser.buf) {
				nal := append([]byte(nil), r.parser.buf[start:]...)
				r.parser.buf = nil
				return nal, nil
			}
			return nil, io.EOF
		}
		n, err := r.reader.Read(r.readBuf)
		if n > 0 {
			r.nals = append(r.nals, r.parser.Push(r.readBuf[:n])...)
		}
		if err != nil {
			if err != io.EOF {
				return nil, err
			}
			r.eof = true
		}
		if n == 0 && err == nil {
			continue
		}
	}
}
