package airplay

import (
	"math"
	"time"
)

const (
	// Arrival delay is measured in windows of host time. The fastest packet of
	// a window has the least scheduling delay, so window minima track the
	// capture clock rather than pipe or scheduler jitter.
	captureDriftWindow = time.Second
	// The first windows establish the delay the source timestamps already
	// account for. Only later changes are treated as clock drift.
	captureDriftBaselineWindows = 3
	// Each new estimate is the lowest of the recent window minima. A stall
	// shorter than this many windows therefore cannot look like drift.
	captureDriftFloorWindows = 3
	// Exponential smoothing, in windows. At a typical 100 ppm mismatch this
	// lags the true drift by about 2 ms and moves the timeline by at most
	// 0.1 ms per second, far below what a receiver hears.
	captureDriftSmoothingWindows = 20
	// A larger change in one window is a clock step (for example a wall-clock
	// correction between GStreamer's NTP mapping and the host), not drift.
	captureDriftStepThreshold = 50 * time.Millisecond
	captureDriftLogInterval   = 5 * time.Millisecond
)

// captureClockDrift keeps source timestamps on the host clock.
//
// GStreamer's pipeline clock is the capture device's own clock when a live
// audio source such as pulsesrc or pipewiresrc provides it, and the RTP/ONVIF
// timestamps are mapped to real time only once, at the first buffer. Every
// sound card runs slightly fast or slow, so those timestamps slowly diverge from
// the host clock that drives AirPlay's network clock and stale-frame checks:
// a slow card eats the playout lead until audio breaks up, and a fast one lets
// audio fall behind video. The arrival time of each packet shows the
// divergence; this estimator measures it and returns the correction to add.
type captureClockDrift struct {
	windowStart time.Time
	windowMin   time.Duration
	haveWindow  bool

	recent      [captureDriftFloorWindows]time.Duration
	recentCount int
	recentNext  int

	windows    int
	baseline   time.Duration
	baselineAt time.Time
	estimate   time.Duration
	correction time.Duration
	lastLogged time.Duration
}

// observe records a packet that arrived at host time arrival and claims to
// have been captured at pts, and returns the correction to add to pts.
func (d *captureClockDrift) observe(arrival, pts time.Time) time.Duration {
	if arrival.IsZero() || pts.IsZero() {
		return d.correction
	}
	delay := arrival.Sub(pts)
	if !d.haveWindow {
		d.haveWindow = true
		d.windowStart = arrival
		d.windowMin = delay
		return d.correction
	}
	if elapsed := arrival.Sub(d.windowStart); elapsed >= captureDriftWindow || elapsed < 0 {
		d.closeWindow(arrival)
		d.windowStart = arrival
		d.windowMin = delay
	} else if delay < d.windowMin {
		d.windowMin = delay
	}
	return d.correction
}

func (d *captureClockDrift) closeWindow(now time.Time) {
	d.recent[d.recentNext] = d.windowMin
	d.recentNext = (d.recentNext + 1) % len(d.recent)
	if d.recentCount < len(d.recent) {
		d.recentCount++
	}
	floor := d.recent[0]
	for _, value := range d.recent[1:d.recentCount] {
		if value < floor {
			floor = value
		}
	}

	d.windows++
	if d.windows <= captureDriftBaselineWindows {
		d.baseline = floor
		d.baselineAt = now
		d.estimate = floor
		return
	}

	change := floor - d.estimate
	if change > captureDriftStepThreshold || change < -captureDriftStepThreshold {
		dbg("[AUDIO] capture clock stepped by %v; re-anchoring source timestamps", change)
		d.estimate = floor
	} else {
		d.estimate += time.Duration(math.Round(float64(change) / captureDriftSmoothingWindows))
	}
	d.correction = d.estimate - d.baseline

	if diff := d.correction - d.lastLogged; diff >= captureDriftLogInterval || diff <= -captureDriftLogInterval {
		d.lastLogged = d.correction
		ppm := 0.0
		if span := now.Sub(d.baselineAt); span > 0 {
			ppm = float64(d.correction) / float64(span) * 1e6
		}
		dbg("[AUDIO] capture clock drift correction %v (about %+.0f ppm vs host clock)", d.correction, -ppm)
	}
}
