//go:build windows || (linux && cgo && desktop)

package main

import (
	"image"
	"testing"
	"time"

	"doubletake/internal/daemon"
	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

type discoveryFixture struct {
	desktopBackend
	data     snapshot
	selected deviceKey
}

func (f *discoveryFixture) state() snapshot { return f.data }

func (f *discoveryFixture) connect(ip string, port int, id string) daemon.Response {
	f.selected = deviceKey{ip: ip, port: port, id: id}
	return daemon.Response{OK: true}
}

func TestDesktopSelectsTheRenderedReceiver(t *testing.T) {
	device := daemon.DeviceInfo{Name: "Test Apple TV", IP: "192.0.2.1", Port: 7000, DeviceID: "00:11:22:33:44:55"}
	f := &discoveryFixture{data: snapshot{Devices: []daemon.DeviceInfo{device}}}
	d := newDesktop(f, "en", true)
	var router input.Router
	var ops op.Ops
	gtx := layout.Context{
		Constraints: layout.Exact(image.Pt(1120, 760)),
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Now:         time.Now(), Source: router.Source(), Ops: &ops,
	}
	render := func() {
		ops.Reset()
		d.layout(gtx)
		router.Frame(&ops)
	}
	render()

	// Discovery changed before the next draw. A button from the previous
	// frame still refers to the old receiver, even at the same IP address.
	f.data.Devices = []daemon.DeviceInfo{{
		Name: device.Name, IP: device.IP, Port: device.Port, DeviceID: "00:11:22:33:44:66",
	}}
	gtx.Now = gtx.Now.Add(time.Second)
	router.Queue(
		pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: f32.Pt(90, 290)},
		pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: f32.Pt(90, 290)},
	)
	render()
	if want := receiverKey(device); f.selected != want {
		t.Fatalf("selected %+v, want %+v", f.selected, want)
	}
}

func TestDesktopRedrawsContinuouslyOnlyForAnimations(t *testing.T) {
	device := daemon.DeviceInfo{Name: "Test Apple TV", IP: "192.0.2.1", Port: 7000, DeviceID: "00:11:22:33:44:55"}
	f := &discoveryFixture{}
	d := newDesktop(f, "en", true)
	var ops op.Ops
	now := time.Now()
	frame := func() time.Duration {
		now = now.Add(statePollInterval) // each frame also polls the daemon
		ops.Reset()
		gtx := layout.Context{
			Constraints: layout.Exact(image.Pt(1120, 760)),
			Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
			Now:         now, Ops: &ops,
		}
		d.layout(gtx)
		return d.nextFrame(now).Sub(now)
	}

	if got := frame(); got != radarFrameRate {
		t.Fatalf("searching with the radar visible: next frame in %v, want %v", got, radarFrameRate)
	}

	f.data = snapshot{Devices: []daemon.DeviceInfo{device}}
	if got := frame(); got != statePollInterval {
		t.Fatalf("receivers listed, nothing streaming: next frame in %v, want the %v state poll", got, statePollInterval)
	}

	f.data.Status.Streams = []daemon.StreamInfo{{Device: device.Name, DeviceIP: device.IP, State: daemon.StateStreaming}}
	if got := frame(); got != statePollInterval {
		t.Fatalf("streaming: next frame in %v, want the %v state poll", got, statePollInterval)
	}

	f.data.Status.Streams[0].State = daemon.StateConnecting
	if got := frame(); got != spinnerFrameRate {
		t.Fatalf("connecting: next frame in %v, want spinner rate %v", got, spinnerFrameRate)
	}
}
