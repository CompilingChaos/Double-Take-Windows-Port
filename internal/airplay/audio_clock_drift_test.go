package airplay

import (
	"math/rand"
	"testing"
	"time"
)

const driftTestFrame = 352

// driftTestClocks models a capture device whose clock differs from the host's
// by ppm. pts is what GStreamer reports (device time mapped once to real time);
// arrival is when the packet reaches DoubleTake on the host clock.
type driftTestClocks struct {
	base     time.Time
	ppm      float64
	latency  time.Duration
	rng      *rand.Rand
	lastSeen time.Time
}

func (c *driftTestClocks) packet(i int) (pts, arrival time.Time) {
	device := audioSamplesDuration(uint64(i * driftTestFrame))
	pts = c.base.Add(device)
	captured := c.base.Add(time.Duration(float64(device) / (1 + c.ppm/1e6)))
	jitter := time.Duration(c.rng.Int63n(int64(3 * time.Millisecond)))
	if c.rng.Intn(500) == 0 {
		jitter += 40 * time.Millisecond // an occasional scheduler stall
	}
	arrival = captured.Add(c.latency + jitter)
	if arrival.Before(c.lastSeen) {
		arrival = c.lastSeen // a pipe delivers in order
	}
	c.lastSeen = arrival
	return pts, arrival
}

// undelayedArrival is when packet i would arrive without scheduling jitter.
func (c *driftTestClocks) undelayedArrival(i int) time.Time {
	device := audioSamplesDuration(uint64(i * driftTestFrame))
	return c.base.Add(time.Duration(float64(device)/(1+c.ppm/1e6)) + c.latency)
}

func (c *driftTestClocks) trueDrift(pts time.Time) time.Duration {
	device := pts.Sub(c.base)
	return time.Duration(float64(device)/(1+c.ppm/1e6)) - device
}

func TestCaptureClockDriftTracksDeviceClock(t *testing.T) {
	for _, ppm := range []float64{-150, -50, 0, 50, 150} {
		clocks := &driftTestClocks{base: time.Now(), ppm: ppm, latency: 15 * time.Millisecond, rng: rand.New(rand.NewSource(1))}
		var drift captureClockDrift
		var previous, worstStep, worstError time.Duration
		frames := int((10 * time.Minute).Seconds() * audioSampleRate / driftTestFrame)
		for i := 0; i < frames; i++ {
			pts, arrival := clocks.packet(i)
			correction := drift.observe(arrival, pts)
			if step := (correction - previous).Abs(); step > worstStep {
				worstStep = step
			}
			previous = correction
			if pts.Sub(clocks.base) > time.Minute {
				if e := (correction - clocks.trueDrift(pts)).Abs(); e > worstError {
					worstError = e
				}
			}
		}
		if worstError > 4*time.Millisecond {
			t.Errorf("ppm=%+.0f: correction strayed %v from the true drift", ppm, worstError)
		}
		if worstStep > 500*time.Microsecond {
			t.Errorf("ppm=%+.0f: correction moved %v at once; receivers would hear it", ppm, worstStep)
		}
	}
}

func TestCaptureClockDriftIgnoresJitterWithoutDrift(t *testing.T) {
	clocks := &driftTestClocks{base: time.Now(), latency: 25 * time.Millisecond, rng: rand.New(rand.NewSource(2))}
	var drift captureClockDrift
	for i := 0; i < int(5*60*audioSampleRate/driftTestFrame); i++ {
		pts, arrival := clocks.packet(i)
		if c := drift.observe(arrival, pts).Abs(); c > time.Millisecond {
			t.Fatalf("packet %d: correction %v without any clock drift", i, c)
		}
	}
}

func TestCaptureClockDriftFollowsClockStep(t *testing.T) {
	clocks := &driftTestClocks{base: time.Now(), latency: 15 * time.Millisecond, rng: rand.New(rand.NewSource(3))}
	var drift captureClockDrift
	const step = 200 * time.Millisecond
	var correction time.Duration
	for i := 0; i < int(60*audioSampleRate/driftTestFrame); i++ {
		pts, arrival := clocks.packet(i)
		if pts.Sub(clocks.base) > 20*time.Second {
			pts = pts.Add(-step) // the wall clock moved under GStreamer's mapping
		}
		correction = drift.observe(arrival, pts)
	}
	if (correction - step).Abs() > 2*time.Millisecond {
		t.Fatalf("correction after a %v clock step = %v", step, correction)
	}
}

// steppedL16Source serves a timestamped RTP/L16 capture stream one packet at a
// time and advances the simulated host clock to each packet's arrival.
type steppedL16Source struct {
	clocks  *driftTestClocks
	payload []byte
	next    int
	pending []byte
	now     time.Time
}

func (s *steppedL16Source) Read(p []byte) (int, error) {
	if len(s.pending) == 0 {
		pts, arrival := s.clocks.packet(s.next)
		s.pending = testFramedL16Packet(uint16(s.next), uint32(s.next*driftTestFrame), pts, s.payload)
		s.now = arrival
		s.next++
	}
	n := copy(p, s.pending)
	s.pending = s.pending[n:]
	return n, nil
}

// TestLinuxCaptureKeepsPlayoutLeadUnderClockDrift runs a 15-minute session
// through the RTP/L16 reader, the RTP clock and TimeAnnounce exactly as
// StreamAudio uses them, and checks the time each packet has left before the
// receiver plays it.
func TestLinuxCaptureKeepsPlayoutLeadUnderClockDrift(t *testing.T) {
	const (
		latencySamples = 3748 // 85 ms, PTP receivers
		networkDelay   = 5 * time.Millisecond
	)
	for _, ppm := range []float64{-150, 0, 150} {
		payload, _ := testL16Samples(driftTestFrame, 0)
		source := &steppedL16Source{
			clocks:  &driftTestClocks{base: time.Now(), ppm: ppm, latency: 15 * time.Millisecond, rng: rand.New(rand.NewSource(4))},
			payload: payload,
		}
		reader := newRTPL16PCMFrameReaderWithNow(source, func() time.Time { return source.now })
		clock := newAudioRTPClock(1000)
		frame := make([]byte, driftTestFrame*audioBytesPerSampleFrame)

		var announcedAt time.Time
		var announcedRTP uint32
		var firstSlack time.Duration
		minSlack, maxSlack := time.Hour, -time.Hour
		stale := 0
		frames := int((15 * time.Minute).Seconds() * audioSampleRate / driftTestFrame)
		for i := 0; i < frames; i++ {
			position, err := reader.ReadPCMFramePosition(frame)
			if err != nil {
				t.Fatal(err)
			}
			now := source.now
			if audioFrameIsStale(position.PTS, now, latencySamples, 0) {
				stale++
				continue
			}
			rtp, _ := clock.mapFramePosition(position, driftTestFrame)
			if announcedAt.IsZero() || now.Sub(announcedAt) >= time.Second {
				announcedAt = now
				announcedRTP, _ = clock.rtpAt(now)
			}
			// Receiver: sample announcedRTP-latency plays at announcedAt.
			offset := int64(int32(rtp - (announcedRTP - latencySamples)))
			render := announcedAt.Add(time.Duration(offset) * time.Second / audioSampleRate)
			// Measure against the jitter-free arrival: stalls delay single
			// packets whether or not the clocks drift.
			slack := render.Sub(source.clocks.undelayedArrival(i).Add(networkDelay))
			if i < 1000 {
				firstSlack = slack
				continue
			}
			if slack < minSlack {
				minSlack = slack
			}
			if slack > maxSlack {
				maxSlack = slack
			}
		}
		t.Logf("ppm=%+4.0f playout slack: start=%v range=[%v, %v] stale=%d",
			ppm, firstSlack.Round(time.Millisecond), minSlack.Round(time.Millisecond), maxSlack.Round(time.Millisecond), stale)
		if stale > 0 {
			t.Errorf("ppm=%+.0f: %d frames dropped as stale", ppm, stale)
		}
		// Without correction a 150 ppm card moves this by 135 ms in 15 minutes.
		if minSlack < firstSlack-15*time.Millisecond || maxSlack > firstSlack+15*time.Millisecond {
			t.Errorf("ppm=%+.0f: playout slack wandered to [%v, %v] from %v", ppm, minSlack, maxSlack, firstSlack)
		}
	}
}
