//go:build windows || (linux && cgo && desktop)

package main

import (
	"image"
	"testing"
	"time"

	"doubletake/internal/daemon"
	"doubletake/internal/netdns"
	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

type dnsFixture struct {
	desktopBackend
	data  snapshot
	calls chan bool
}

func (f *dnsFixture) state() snapshot { return f.data }

func (f *dnsFixture) setCloudflareDNS(enable bool) daemon.Response {
	f.calls <- enable
	return daemon.Response{OK: true}
}

func TestDNSChangeWaitsForTheExperimentalWarning(t *testing.T) {
	f := &dnsFixture{data: snapshot{CloudflareDNS: netdns.StateOff}, calls: make(chan bool, 4)}
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
	noCall := func(step string) {
		select {
		case enable := <-f.calls:
			t.Fatalf("%s: DNS changed (enable=%v) before the warning was accepted", step, enable)
		case <-time.After(50 * time.Millisecond):
		}
	}

	render()
	d.cloudflareDNS.Click()
	render()
	if d.dnsPrompt == nil || !*d.dnsPrompt {
		t.Fatal("clicking the DNS button did not show the warning for switching on")
	}
	noCall("warning shown")

	d.dnsCancel.Click()
	render()
	if d.dnsPrompt != nil {
		t.Fatal("Cancel did not close the warning")
	}
	noCall("cancelled")

	d.cloudflareDNS.Click()
	render()
	d.dnsContinue.Click()
	render()
	select {
	case enable := <-f.calls:
		if !enable {
			t.Fatal("Continue switched DNS off instead of on")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Continue did not change DNS")
	}
}
