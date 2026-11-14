//go:build windows

package airplay

import (
	"encoding/binary"
	"io"
	"math"
	"testing"
	"time"
)

const testFrameBytes = 352 * audioBytesPerSampleFrame

func readQueueFrame(t *testing.T, q *timestampedPCMQueue) ([]byte, audioPCMFramePosition) {
	t.Helper()
	frame := make([]byte, testFrameBytes)
	position, err := q.ReadPCMFramePosition(frame)
	if err != nil {
		t.Fatalf("ReadPCMFramePosition: %v", err)
	}
	return frame, position
}

func TestTimestampedPCMQueueReportsSamplePositionAndTime(t *testing.T) {
	q := newTimestampedPCMQueue(time.Second)
	base := time.Now()
	q.push(make([]byte, 2*testFrameBytes), base)

	_, first := readQueueFrame(t, q)
	_, second := readQueueFrame(t, q)
	if !first.HasSourceRTP || first.SourceRTP != 0 || !first.PTS.Equal(base) {
		t.Fatalf("first frame = %+v, want position 0 at %v", first, base)
	}
	if second.SourceRTP != 352 {
		t.Fatalf("second frame position = %d, want 352", second.SourceRTP)
	}
	if got, want := second.PTS.Sub(base), audioSamplesDuration(352); got != want {
		t.Fatalf("second frame PTS offset = %v, want %v", got, want)
	}
}

func TestTimestampedPCMQueueOverflowKeepsPositionsOfNewestAudio(t *testing.T) {
	q := newTimestampedPCMQueue(audioSamplesDuration(1024))
	base := time.Now()
	for i := 0; i < 10; i++ {
		q.push(make([]byte, testFrameBytes), base.Add(audioSamplesDuration(uint64(i*352))))
	}
	_, position := readQueueFrame(t, q)
	if position.SourceRTP < 352*7 {
		t.Fatalf("overflow kept stale audio: read position %d", position.SourceRTP)
	}
	if want := base.Add(audioSamplesDuration(uint64(position.SourceRTP))); position.PTS.Sub(want).Abs() > time.Microsecond {
		t.Fatalf("PTS after overflow = %v, want %v", position.PTS, want)
	}
}

func TestTimestampedPCMQueueCloseUnblocksReader(t *testing.T) {
	q := newTimestampedPCMQueue(time.Second)
	done := make(chan error, 1)
	go func() {
		_, err := q.ReadPCMFramePosition(make([]byte, testFrameBytes))
		done <- err
	}()
	time.Sleep(10 * time.Millisecond)
	q.close(nil)
	select {
	case err := <-done:
		if err != io.EOF {
			t.Fatalf("read after close = %v, want EOF", err)
		}
	case <-time.After(time.Second):
		t.Fatal("reader stayed blocked after close")
	}
}

// sinePackets builds 48 kHz capture packets of a continuous 1 kHz sine.
func sinePacket(startFrame, frames int) []stereoSample {
	out := make([]stereoSample, frames)
	for i := range out {
		v := 0.5 * math.Sin(2*math.Pi*1000*float64(startFrame+i)/48000)
		out[i] = stereoSample{left: v, right: v}
	}
	return out
}

func TestWindowsCaptureTimelineResamplesWithAccurateTimestamps(t *testing.T) {
	q := newTimestampedPCMQueue(10 * time.Second)
	timeline := newWindowsCaptureTimeline(48000, q)
	base := time.Now()
	const packet = 480
	for i := 0; i < 200; i++ { // two seconds
		pts := base.Add(time.Duration(i*packet) * time.Second / 48000)
		timeline.push(sinePacket(i*packet, packet), pts, true)
	}
	frames := 0
	for len(q.pcm) >= testFrameBytes {
		frame, position := readQueueFrame(t, q)
		want := base.Add(audioSamplesDuration(uint64(position.SourceRTP)))
		if d := position.PTS.Sub(want).Abs(); d > 50*time.Microsecond {
			t.Fatalf("frame %d PTS off by %v", frames, d)
		}
		// The resampled sine must stay continuous across packet boundaries.
		for i := 0; i < 352; i++ {
			sample := int16(binary.LittleEndian.Uint16(frame[i*4:]))
			at := float64(uint64(position.SourceRTP)+uint64(i)) / audioSampleRate
			expected := 0.5 * math.Sin(2*math.Pi*1000*at) * 32767
			if math.Abs(float64(sample)-expected) > 600 {
				t.Fatalf("frame %d sample %d = %d, want about %.0f", frames, i, sample, expected)
			}
		}
		frames++
	}
	if got := frames * 352; got < 88000 || got > 88200 {
		t.Fatalf("two seconds produced %d samples, want about 88200", got)
	}
}

func TestWindowsCaptureTimelineFillsLostPacketWithSilence(t *testing.T) {
	q := newTimestampedPCMQueue(10 * time.Second)
	timeline := newWindowsCaptureTimeline(48000, q)
	base := time.Now()
	const packet = 480
	for i := 0; i < 100; i++ {
		if i == 50 {
			continue // a packet the capture thread never received
		}
		pts := base.Add(time.Duration(i*packet) * time.Second / 48000)
		timeline.push(sinePacket(i*packet, packet), pts, true)
	}
	if timeline.gaps != 1 {
		t.Fatalf("gaps = %d, want 1", timeline.gaps)
	}
	samples := len(q.pcm) / audioBytesPerSampleFrame
	// One second of input minus the resampler's one-sample look-ahead.
	if samples < 44090 || samples > 44100 {
		t.Fatalf("one second with a lost packet produced %d samples, want about 44100", samples)
	}
	// Audio after the gap keeps its original timing.
	for len(q.pcm) >= testFrameBytes {
		_, position := readQueueFrame(t, q)
		want := base.Add(audioSamplesDuration(uint64(position.SourceRTP)))
		if d := position.PTS.Sub(want).Abs(); d > 50*time.Microsecond {
			t.Fatalf("PTS at position %d off by %v", position.SourceRTP, d)
		}
	}
}

func TestWindowsCaptureTimelineIdleFillKeepsStreamRunning(t *testing.T) {
	q := newTimestampedPCMQueue(10 * time.Second)
	timeline := newWindowsCaptureTimeline(48000, q)
	start := time.Now()
	timeline.fillIdle(start)
	timeline.fillIdle(start.Add(500 * time.Millisecond))
	samples := len(q.pcm) / audioBytesPerSampleFrame
	if want := durationToAudioSamples(500 * time.Millisecond); math.Abs(float64(int64(samples)-want)) > 50 {
		t.Fatalf("idle fill produced %d samples, want about %d", samples, want)
	}
	// A real packet arriving afterwards continues the same timeline.
	pts := start.Add(500 * time.Millisecond)
	timeline.push(sinePacket(0, 480), pts, true)
	_, position := readQueueFrame(t, q)
	if d := position.PTS.Sub(start.Add(-windowsCaptureIdleMargin)).Abs(); d > time.Millisecond {
		t.Fatalf("idle stream starts %v from its expected time", d)
	}
}

func TestCapturePacketPTSRejectsUnusableTimestamps(t *testing.T) {
	now := time.Now()
	if _, ok := capturePacketPTS(now, 1_000_000_000, 0, false); ok {
		t.Fatal("zero device timestamp accepted")
	}
	if _, ok := capturePacketPTS(now, 1_000_000_000, 999_900_000, true); ok {
		t.Fatal("timestamp flagged as erroneous accepted")
	}
	pts, ok := capturePacketPTS(now, 1_000_000_000, 999_900_000, false)
	if !ok || now.Sub(pts) != 10*time.Millisecond {
		t.Fatalf("PTS = %v (ok=%t), want 10ms before now", now.Sub(pts), ok)
	}
}
