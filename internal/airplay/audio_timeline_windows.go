//go:build windows

package airplay

import (
	"io"
	"math"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// A device timestamp within this distance of the sample-count prediction is
	// ordinary clock drift and only rebases the timeline.
	windowsCaptureTimestampTolerance = 5 * time.Millisecond
	// Lost capture periods up to this long are replaced by silence so the
	// receiver sees one continuous stream. A longer stall becomes a source gap.
	windowsCaptureMaxGapFill = time.Second
	// An endpoint with no active render stream delivers no loopback packets.
	// After this much quiet the capture thread synthesizes the missing silence,
	// staying far enough behind real time that the next device packet follows it.
	windowsCaptureIdleThreshold = 100 * time.Millisecond
	windowsCaptureIdleMargin    = 40 * time.Millisecond
)

// timestampedPCMQueue is the boundary between the WASAPI capture thread and the
// AirPlay audio loop. It holds 44.1 kHz interleaved stereo S16LE and knows the
// presentation time of every sample, so the capture thread never waits for the
// network sender and the sender never has to guess when a sample was recorded.
type timestampedPCMQueue struct {
	mu       sync.Mutex
	ready    *sync.Cond
	pcm      []byte
	position uint64    // sample position of pcm[0]
	origin   time.Time // presentation time of sample position 0
	limit    int       // maximum buffered bytes
	closed   bool
	err      error
	overruns uint64
}

func newTimestampedPCMQueue(capacity time.Duration) *timestampedPCMQueue {
	limit := int(durationToAudioSamples(capacity)) * audioBytesPerSampleFrame
	if limit < 4096 {
		limit = 4096
	}
	q := &timestampedPCMQueue{limit: limit}
	q.ready = sync.NewCond(&q.mu)
	return q
}

// push appends PCM whose first sample was presented at firstPTS. The newest
// correlation replaces the previous one, which follows slow drift between the
// audio device clock and the host clock without disturbing sample positions.
func (q *timestampedPCMQueue) push(pcm []byte, firstPTS time.Time) {
	if len(pcm) == 0 {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	end := q.position + uint64(len(q.pcm)/audioBytesPerSampleFrame)
	q.origin = firstPTS.Add(-audioSamplesDuration(end))
	q.pcm = append(q.pcm, pcm...)
	if excess := len(q.pcm) - q.limit; excess > 0 {
		// The sender stalled for longer than the queue covers. Discard the oldest
		// samples; their positions remain accounted for, so the loss reaches the
		// receiver as a timestamp gap instead of as permanently late audio.
		excess += (audioBytesPerSampleFrame - excess%audioBytesPerSampleFrame) % audioBytesPerSampleFrame
		copy(q.pcm, q.pcm[excess:])
		q.pcm = q.pcm[:len(q.pcm)-excess]
		q.position += uint64(excess / audioBytesPerSampleFrame)
		q.overruns++
	}
	q.ready.Broadcast()
}

// skip abandons buffered audio and advances the sample position by an
// additional number of samples which were never captured.
func (q *timestampedPCMQueue) skip(samples uint64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.position += uint64(len(q.pcm)/audioBytesPerSampleFrame) + samples
	q.pcm = q.pcm[:0]
}

func (q *timestampedPCMQueue) close(err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.closed = true
	q.err = err
	q.ready.Broadcast()
}

func (q *timestampedPCMQueue) ReadPCMFrame(dst []byte) (time.Time, error) {
	position, err := q.ReadPCMFramePosition(dst)
	return position.PTS, err
}

func (q *timestampedPCMQueue) ReadPCMFramePosition(dst []byte) (audioPCMFramePosition, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.pcm) < len(dst) && !q.closed {
		q.ready.Wait()
	}
	if len(q.pcm) < len(dst) {
		if q.err != nil {
			return audioPCMFramePosition{}, q.err
		}
		return audioPCMFramePosition{}, io.EOF
	}
	position := q.position
	copy(dst, q.pcm[:len(dst)])
	copy(q.pcm, q.pcm[len(dst):])
	q.pcm = q.pcm[:len(q.pcm)-len(dst)]
	q.position += uint64(len(dst) / audioBytesPerSampleFrame)
	return audioPCMFramePosition{
		PTS:          q.origin.Add(audioSamplesDuration(position)),
		SourceRTP:    uint32(position),
		HasSourceRTP: true,
	}, nil
}

// windowsCaptureTimeline turns WASAPI packets into one continuous 44.1 kHz
// stream. Sample counting defines its length; the device timestamp of each
// packet defines where it lies in time, which is what exposes a lost packet.
type windowsCaptureTimeline struct {
	inRate    float64
	resampler audioResampler
	queue     *timestampedPCMQueue

	started    bool
	basePTS    time.Time // device time of the first input frame counted in baseFrames
	baseFrames uint64    // input frames accepted since basePTS
	gaps       uint64
}

func newWindowsCaptureTimeline(inRate uint32, queue *timestampedPCMQueue) *windowsCaptureTimeline {
	rate := float64(inRate)
	if rate <= 0 {
		rate = audioSampleRate
	}
	return &windowsCaptureTimeline{inRate: rate, resampler: audioResampler{inRate: rate}, queue: queue}
}

func (t *windowsCaptureTimeline) inputDuration(frames uint64) time.Duration {
	return time.Duration(math.Round(float64(frames) / t.inRate * float64(time.Second)))
}

func (t *windowsCaptureTimeline) inputFrames(d time.Duration) int {
	return int(math.Round(d.Seconds() * t.inRate))
}

// nextInputPTS is the device time at which the next input frame is expected.
func (t *windowsCaptureTimeline) nextInputPTS() time.Time {
	return t.basePTS.Add(t.inputDuration(t.baseFrames))
}

// push accepts one capture packet. pts is the device time of its first frame;
// trusted is false when Windows supplied no usable timestamp for the packet,
// in which case the packet simply continues the sample-counted timeline.
func (t *windowsCaptureTimeline) push(samples []stereoSample, pts time.Time, trusted bool) {
	if len(samples) == 0 {
		return
	}
	if !t.started {
		t.started = true
		t.basePTS = pts
		t.baseFrames = 0
	} else if trusted {
		delta := pts.Sub(t.nextInputPTS())
		switch {
		case delta > windowsCaptureMaxGapFill || delta < -windowsCaptureMaxGapFill:
			t.restart(pts, delta)
		case delta > windowsCaptureTimestampTolerance:
			t.gaps++
			dbg("[AUDIO] capture lost %v of audio; inserting silence (gap %d)", delta, t.gaps)
			t.feed(make([]stereoSample, t.inputFrames(delta)))
		case delta < -windowsCaptureTimestampTolerance:
			overlap := t.inputFrames(-delta)
			if overlap >= len(samples) {
				return
			}
			samples = samples[overlap:]
			pts = pts.Add(t.inputDuration(uint64(overlap)))
		}
		// Rebase on the device clock. Sample positions already produced are
		// unaffected; only the time attributed to the following samples moves.
		t.basePTS = pts
		t.baseFrames = 0
	}
	t.feed(samples)
}

// fillIdle keeps the stream running while the device delivers nothing.
func (t *windowsCaptureTimeline) fillIdle(now time.Time) {
	target := now.Add(-windowsCaptureIdleMargin)
	if !t.started {
		t.started = true
		t.basePTS = target
		t.baseFrames = 0
		return
	}
	next := t.nextInputPTS()
	if now.Sub(next) < windowsCaptureIdleThreshold {
		return
	}
	if frames := t.inputFrames(target.Sub(next)); frames > 0 {
		t.feed(make([]stereoSample, frames))
	}
}

func (t *windowsCaptureTimeline) restart(pts time.Time, delta time.Duration) {
	dbg("[AUDIO] capture clock jumped by %v; restarting the audio timeline", delta)
	var skipped uint64
	if delta > 0 {
		skipped = uint64(durationToAudioSamples(delta))
	}
	t.resampler = audioResampler{inRate: t.inRate}
	t.queue.skip(skipped)
}

func (t *windowsCaptureTimeline) feed(samples []stereoSample) {
	if len(samples) == 0 {
		return
	}
	// The resampler's next output lies r.phase input frames into its pending
	// buffer, i.e. this far from the first frame appended now.
	offsetFrames := t.resampler.phase - float64(len(t.resampler.buf))
	firstPTS := t.nextInputPTS().Add(time.Duration(math.Round(offsetFrames / t.inRate * float64(time.Second))))
	pcm := t.resampler.resampleToPCM(samples)
	t.baseFrames += uint64(len(samples))
	t.queue.push(pcm, firstPTS)
}

var (
	kernel32PerformanceDLL        = windows.NewLazySystemDLL("kernel32.dll")
	procQueryPerformanceCounter   = kernel32PerformanceDLL.NewProc("QueryPerformanceCounter")
	procQueryPerformanceFrequency = kernel32PerformanceDLL.NewProc("QueryPerformanceFrequency")
	performanceFrequencyOnce      sync.Once
	performanceFrequency          int64

	avrtDLL                            = windows.NewLazySystemDLL("avrt.dll")
	procAvSetMmThreadCharacteristicsW  = avrtDLL.NewProc("AvSetMmThreadCharacteristicsW")
	procAvRevertMmThreadCharacteristic = avrtDLL.NewProc("AvRevertMmThreadCharacteristics")
)

// performanceCounter100ns reads the clock WASAPI stamps capture packets with,
// in the same 100-nanosecond units.
func performanceCounter100ns() int64 {
	performanceFrequencyOnce.Do(func() {
		procQueryPerformanceFrequency.Call(uintptr(unsafe.Pointer(&performanceFrequency)))
	})
	if performanceFrequency <= 0 {
		return 0
	}
	var counter int64
	procQueryPerformanceCounter.Call(uintptr(unsafe.Pointer(&counter)))
	return counter/performanceFrequency*10_000_000 + counter%performanceFrequency*10_000_000/performanceFrequency
}

// capturePacketPTS converts a packet's device timestamp to the Go monotonic
// clock. now and nowCounter must be read together.
func capturePacketPTS(now time.Time, nowCounter int64, packetCounter uint64, timestampError bool) (time.Time, bool) {
	if timestampError || packetCounter == 0 || nowCounter == 0 {
		return time.Time{}, false
	}
	age := time.Duration(nowCounter-int64(packetCounter)) * 100
	if age < -time.Second || age > time.Minute {
		return time.Time{}, false
	}
	return now.Add(-age), true
}

// raiseCaptureThreadPriority registers the locked capture thread with the
// multimedia scheduler. Process loopback keeps only about one period of audio,
// so a descheduled capture thread loses samples.
func raiseCaptureThreadPriority() func() {
	if avrtDLL.Load() != nil || procAvSetMmThreadCharacteristicsW.Find() != nil {
		return func() {}
	}
	task, err := windows.UTF16PtrFromString("Pro Audio")
	if err != nil {
		return func() {}
	}
	var index uint32
	handle, _, _ := procAvSetMmThreadCharacteristicsW.Call(uintptr(unsafe.Pointer(task)), uintptr(unsafe.Pointer(&index)))
	if handle == 0 {
		return func() {}
	}
	return func() { procAvRevertMmThreadCharacteristic.Call(handle) }
}
