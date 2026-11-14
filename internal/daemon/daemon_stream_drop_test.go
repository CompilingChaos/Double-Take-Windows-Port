package daemon

import (
	"context"
	"io"
	"log"
	"net"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"doubletake/internal/airplay"
)

// TestDaemonReportsAStreamTheReceiverDropped checks that a stream which dies
// on its own, here because the receiver shuts down, leaves an error behind
// instead of silently disappearing from the status.
func TestDaemonReportsAStreamTheReceiverDropped(t *testing.T) {
	d, target, stopReceiver := startStreamingToTestReceiver(t)

	// Switch the receiver off mid-stream.
	stopReceiver()

	status := waitForStatus(t, d, 15*time.Second, "the dropped stream to be reported", func(resp Response) bool {
		return len(resp.Streams) == 0 && resp.Error != ""
	})
	if !strings.Contains(status.Error, target) || !strings.Contains(status.Error, "mirroring stopped") {
		t.Fatalf("error = %q, want it to name %s and say mirroring stopped", status.Error, target)
	}
}

func TestDaemonDoesNotReportAUserDisconnectAsAnError(t *testing.T) {
	d, target, _ := startStreamingToTestReceiver(t)

	if resp := d.Execute(Request{Cmd: "disconnect", Target: target}); !resp.OK {
		t.Fatalf("disconnect: %+v", resp)
	}
	// Give an erroneous report time to appear before checking for none.
	time.Sleep(500 * time.Millisecond)
	if resp := d.Execute(Request{Cmd: "status"}); len(resp.Streams) != 0 || resp.Error != "" {
		t.Fatalf("status after a user disconnect = %+v, want no streams and no error", resp)
	}
}

// startStreamingToTestReceiver starts a local test receiver and a daemon that
// mirrors a synthetic capture to it. It returns once the stream is live, with
// a function that shuts the receiver down.
func startStreamingToTestReceiver(t *testing.T) (*Daemon, string, func()) {
	t.Helper()
	hwaccel := "none"
	if runtime.GOOS == "windows" {
		if _, err := exec.LookPath("ffmpeg"); err != nil {
			t.Skip("FFmpeg is required for the synthetic capture")
		}
	} else {
		if _, err := exec.LookPath("gst-launch-1.0"); err != nil {
			t.Skip("gst-launch-1.0 is required for the synthetic capture")
		}
		if err := exec.Command("gst-inspect-1.0", "openh264enc").Run(); err != nil {
			t.Skip("GStreamer openh264enc is required for the synthetic capture")
		}
		hwaccel = "openh264"
	}

	receiver, err := airplay.NewReceiverServer(airplay.ReceiverConfig{
		ListenAddress: "127.0.0.1:0",
		Profile:       airplay.ReceiverProfileRoku,
		Auth:          airplay.ReceiverAuthNone,
		Name:          "Test TV",
		Logger:        log.New(io.Discard, "", 0),
	})
	if err != nil {
		t.Fatalf("start receiver: %v", err)
	}
	receiverCtx, cancelReceiver := context.WithCancel(context.Background())
	receiverDone := make(chan error, 1)
	go func() { receiverDone <- receiver.Serve(receiverCtx) }()
	var stopOnce sync.Once
	stopReceiver := func() {
		stopOnce.Do(func() {
			cancelReceiver()
			_ = receiver.Close()
			<-receiverDone
		})
	}
	t.Cleanup(stopReceiver)

	d, err := New(Config{
		CredBackend: "file",
		CredFile:    filepath.Join(t.TempDir(), "credentials.json"),
		FPS:         10,
		Bitrate:     500,
		HWAccel:     hwaccel,
		TestMode:    true,
		NoAudio:     true,
	})
	if err != nil {
		t.Fatalf("create daemon: %v", err)
	}
	t.Cleanup(d.Shutdown)

	addr := receiver.Addr().(*net.TCPAddr)
	target := addr.IP.String()
	if resp := d.Execute(Request{Cmd: "connect", Target: target, Port: addr.Port}); !resp.OK {
		t.Fatalf("connect: %+v", resp)
	}
	waitForStatus(t, d, 20*time.Second, "the stream to start", func(resp Response) bool {
		return len(resp.Streams) == 1 && resp.Streams[0].State == StateStreaming
	})
	return d, target, stopReceiver
}

func waitForStatus(t *testing.T, d *Daemon, timeout time.Duration, what string, ready func(Response) bool) Response {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		resp := d.Execute(Request{Cmd: "status"})
		if ready(resp) {
			return resp
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s; last status: %+v", what, resp)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestStatusGivesEachRecordedErrorItsOwnID(t *testing.T) {
	d := &Daemon{streams: map[string]*activeStream{}}
	if resp := d.Execute(Request{Cmd: "status"}); resp.Error != "" || resp.ErrorID != 0 {
		t.Fatalf("fresh daemon status = %+v, want no error", resp)
	}

	d.mu.Lock()
	d.recordErrorLocked("192.0.2.1: connect failed", "192.0.2.1")
	d.mu.Unlock()
	first := d.Execute(Request{Cmd: "status"})

	// The same failure again, as when a retry fails the same way.
	d.mu.Lock()
	d.clearLastErrorForTargetLocked("192.0.2.1")
	d.recordErrorLocked("192.0.2.1: connect failed", "192.0.2.1")
	d.mu.Unlock()
	second := d.Execute(Request{Cmd: "status"})

	if first.Error != second.Error || first.ErrorID == 0 || second.ErrorID == first.ErrorID {
		t.Fatalf("repeated failure: first=%q/%d second=%q/%d, want the same message under a new ID",
			first.Error, first.ErrorID, second.Error, second.ErrorID)
	}

	d.mu.Lock()
	d.clearLastErrorForTargetLocked("192.0.2.1")
	d.mu.Unlock()
	if cleared := d.Execute(Request{Cmd: "status"}); cleared.Error != "" || cleared.ErrorID != 0 {
		t.Fatalf("status after clearing = %q/%d, want no error", cleared.Error, cleared.ErrorID)
	}
}
