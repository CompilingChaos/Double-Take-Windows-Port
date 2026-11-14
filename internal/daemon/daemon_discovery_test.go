package daemon

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"doubletake/internal/airplay"
)

func TestBackgroundDiscoveryWaitsForEachCycleAfterAFailure(t *testing.T) {
	const cycle = 50 * time.Millisecond
	var scans atomic.Int32
	d := &Daemon{
		deviceLastSeen: make(map[string]time.Time),
		discoveryCycle: cycle,
		// Fail at once, as the resolver does when no interface is up.
		discover: func(context.Context) ([]airplay.AirPlayDevice, error) {
			scans.Add(1)
			return nil, errors.New("no multicast interface")
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*cycle+cycle/2)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.backgroundDiscover(ctx)
	}()
	<-done

	// One scan per cycle: 7 starts fit in 6.5 cycles. Without the wait the
	// loop retries thousands of times in the same period.
	if got := scans.Load(); got < 5 || got > 7 {
		t.Fatalf("discovery scanned %d times in 6.5 cycles, want one scan per cycle", got)
	}
}

func TestBackgroundDiscoveryKeepsTheCycleWhenAScanReturnsEarly(t *testing.T) {
	const cycle = 50 * time.Millisecond
	device := airplay.AirPlayDevice{Name: "TV", IP: "192.0.2.10", Port: 7000}
	var scans atomic.Int32
	d := &Daemon{
		deviceLastSeen: make(map[string]time.Time),
		discoveryCycle: cycle,
		discover: func(context.Context) ([]airplay.AirPlayDevice, error) {
			scans.Add(1)
			return []airplay.AirPlayDevice{device}, nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*cycle+cycle/2)
	defer cancel()
	d.backgroundDiscover(ctx)

	if got := scans.Load(); got < 3 || got > 5 {
		t.Fatalf("discovery scanned %d times in 4.5 cycles, want one scan per cycle", got)
	}
	if devices := d.Execute(Request{Cmd: "devices"}).Devices; len(devices) != 1 || devices[0].IP != device.IP {
		t.Fatalf("devices = %+v, want the discovered receiver", devices)
	}
}
