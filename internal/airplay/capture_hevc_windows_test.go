//go:build windows

package airplay

import (
	"context"
	"testing"
	"time"
)

func TestFFmpegHEVCTestCaptureProducesSampleDescription(t *testing.T) {
	if _, err := ffmpegExecutable(); err != nil {
		t.Skipf("FFmpeg unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	capture, err := StartTestCapture(ctx, CaptureConfig{
		FPS: 5, Bitrate: 1000, HWAccel: "none", VideoCodec: VideoCodecHEVC,
		MaxWidth: 320, MaxHeight: 180,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer capture.Stop()

	var vps, sps, pps []byte
	for frame := 0; frame < 20 && (len(vps) == 0 || len(sps) == 0 || len(pps) == 0); frame++ {
		unit, readErr := capture.ReadVideoAccessUnit()
		if readErr != nil {
			t.Fatalf("read HEVC access unit: %v", readErr)
		}
		for _, wrapped := range splitAnnexBAccessUnit(unit.AnnexB) {
			raw := stripStartCode(wrapped)
			switch hevcNALType(raw) {
			case 32:
				vps = append([]byte(nil), raw...)
			case 33:
				sps = append([]byte(nil), raw...)
			case 34:
				pps = append([]byte(nil), raw...)
			}
		}
	}
	description, err := buildHEVCSampleDescription(vps, sps, pps)
	if err != nil {
		t.Fatalf("build HEVC sample description: %v", err)
	}
	if len(description) < 94 || string(description[4:8]) != "hvc1" || string(description[90:94]) != "hvcC" {
		t.Fatalf("invalid HEVC sample description: %x", description[:min(len(description), 128)])
	}
}
