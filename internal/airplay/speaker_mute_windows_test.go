//go:build windows

package airplay

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSplitStaleSpeakerMuteMarkersKeepsLiveOwners(t *testing.T) {
	markers := []speakerMuteMarker{
		{PID: 10, Device: "speakers"},  // this process
		{PID: 20, Device: "speakers"},  // another running DoubleTake
		{PID: 30, Device: "speakers"},  // crashed
		{PID: 31, Device: "speakers"},  // crashed, same endpoint
		{PID: 40, Device: "headphones"}, // crashed
		{PID: 50, Device: ""},          // malformed
	}
	running := func(pid int) bool { return pid == 20 }
	stale, keep := splitStaleSpeakerMuteMarkers(markers, 10, running)
	wantStale := []speakerMuteMarker{{PID: 30, Device: "speakers"}, {PID: 40, Device: "headphones"}}
	wantKeep := []speakerMuteMarker{{PID: 10, Device: "speakers"}, {PID: 20, Device: "speakers"}}
	if !reflect.DeepEqual(stale, wantStale) {
		t.Fatalf("stale = %+v, want %+v", stale, wantStale)
	}
	if !reflect.DeepEqual(keep, wantKeep) {
		t.Fatalf("keep = %+v, want %+v", keep, wantKeep)
	}
}

func TestSpeakerMuteMarkersRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "DoubleTake", "speaker-mute.json")
	previous := speakerMuteMarkerPath
	speakerMuteMarkerPath = func() string { return path }
	defer func() { speakerMuteMarkerPath = previous }()

	recordSpeakerMute("speakers")
	recordSpeakerMute("speakers") // recording twice keeps one entry
	recordSpeakerMute("headphones")
	got := readSpeakerMuteMarkers()
	want := []speakerMuteMarker{{PID: os.Getpid(), Device: "speakers"}, {PID: os.Getpid(), Device: "headphones"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("markers = %+v, want %+v", got, want)
	}

	clearSpeakerMute("speakers")
	clearSpeakerMute("headphones")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("record still exists after every mute was cleared: %v", err)
	}
}

func TestDoubleTakeProcessRunningRejectsEndedAndForeignProcesses(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "exit", "0")
	if err := cmd.Run(); err != nil {
		t.Skipf("cannot start a short-lived process: %v", err)
	}
	if doubleTakeProcessRunning(cmd.Process.Pid) {
		t.Fatal("ended process reported as running")
	}
	// The test binary is running, but it is not a DoubleTake executable, which
	// is how a reused process ID looks.
	if doubleTakeProcessRunning(os.Getpid()) {
		t.Fatal("non-DoubleTake process reported as a running DoubleTake")
	}
}
