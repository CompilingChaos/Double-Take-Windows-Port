//go:build windows || (linux && cgo && desktop)

package main

import (
	"image"
	"strings"
	"testing"
	"time"

	"doubletake/internal/daemon"
	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

func TestExplainErrorUsesPlainLanguage(t *testing.T) {
	for _, test := range []struct {
		raw, title string
	}{
		{"192.168.1.24: mirroring stopped: video data channel closed: EOF", "Mirroring stopped"},
		{"1920x1080 capture failed: capture exited: exit status 1", "Screen capture stopped"},
		{"FFmpeg was not found. Install it with winget install Gyan.FFmpeg.", "FFmpeg is missing"},
		{"192.168.1.24: connect to 192.168.1.24:7000 failed: dial tcp 192.168.1.24:7000: connectex: No connection could be made because the target machine actively refused it.", "Couldn't reach the receiver"},
		{"192.168.1.24: pin pairing: pair-setup M4: invalid PIN", "Pairing failed"},
		{"192.168.1.24: Living Room did not prove it is the receiver this machine paired with; refusing to cast.", "Receiver identity check failed"},
		{"install global Enter keyboard hook: Access is denied.", "Freeze shortcut limited"},
		{"already connected or connecting to 192.168.1.24", "Already connected"},
		{"something nobody anticipated", "Something went wrong"},
	} {
		title, explanation := explainError(test.raw, false)
		if title != test.title {
			t.Errorf("explainError(%q) title = %q, want %q", test.raw, title, test.title)
		}
		if explanation == "" || strings.Contains(explanation, test.raw) {
			t.Errorf("explainError(%q) explanation = %q, want a plain-language sentence", test.raw, explanation)
		}
		if titleDE, explanationDE := explainError(test.raw, true); titleDE == title || explanationDE == explanation {
			t.Errorf("explainError(%q) is not translated: %q / %q", test.raw, titleDE, explanationDE)
		}
	}
}

func TestRaiseErrorNamesTheReceiverAndCollapsesRepeats(t *testing.T) {
	d := newDesktop(&discoveryFixture{}, "en", true)
	d.state.Devices = []daemon.DeviceInfo{{Name: "Living Room", IP: "192.168.1.24"}}
	now := time.Now()
	raw := "192.168.1.24: mirroring stopped: video data channel closed: EOF"

	d.raiseError(now, raw)
	d.raiseError(now, "an unrelated failure")
	d.raiseError(now.Add(time.Second), raw)

	if len(d.errors) != 2 {
		t.Fatalf("pop-ups = %d, want 2 with the repeat collapsed", len(d.errors))
	}
	newest := d.errors[len(d.errors)-1]
	if newest.raw != raw || newest.count != 2 {
		t.Fatalf("newest pop-up = %q x%d, want the repeated error counted twice", newest.raw, newest.count)
	}
	if newest.receiver != "Living Room · 192.168.1.24" {
		t.Fatalf("receiver = %q, want the receiver's name and address", newest.receiver)
	}

	for i := range maxErrorPopups + 2 {
		d.raiseError(now, strings.Repeat("x", i+1))
	}
	if len(d.errors) != maxErrorPopups {
		t.Fatalf("pop-ups = %d, want at most %d on screen", len(d.errors), maxErrorPopups)
	}
}

func TestErrorPopupCopiesAReportAndDismisses(t *testing.T) {
	f := &discoveryFixture{data: snapshot{Devices: []daemon.DeviceInfo{{Name: "Living Room", IP: "192.168.1.24", Port: 7000}}}}
	d := newDesktop(f, "en", true)
	raw := "192.168.1.24: mirroring stopped: video data channel closed: EOF"
	// The daemon reports the failure through status, as it does at runtime.
	f.data.Status = daemon.Response{Error: raw, ErrorID: 1}

	var router input.Router
	var ops op.Ops
	now := time.Now()
	size := image.Pt(1120, 760)
	render := func() {
		ops.Reset()
		gtx := layout.Context{
			Constraints: layout.Exact(size),
			Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
			Now:         now, Source: router.Source(), Ops: &ops,
		}
		d.layout(gtx)
		router.Frame(&ops)
		now = now.Add(time.Second)
	}
	render()
	if len(d.errors) != 1 {
		t.Fatalf("pop-ups after the daemon error = %d, want 1", len(d.errors))
	}
	// The same error ID on the next poll must not pop up again.
	render()
	if len(d.errors) != 1 || d.errors[0].count != 1 {
		t.Fatalf("pop-ups after a repeated poll = %d (count %d), want the error shown once", len(d.errors), d.errors[0].count)
	}

	click := func(p f32.Point) {
		router.Queue(
			pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: p},
			pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: p},
		)
		render()
	}
	popup := d.errors[0]
	measure := layout.Context{Constraints: layout.Exact(size), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Ops: new(op.Ops)}
	height := d.errorPopupHeight(measure, popup, 420)
	top := 760 - 21 - height
	buttonsY := float32(top + height - popupPad - popupButtonH/2)
	left := float32(1120 - 21 - 420 + popupPad)

	click(f32.Pt(left+20, buttonsY))
	mime, content, ok := router.WriteClipboard()
	if !ok || mime != "application/text" {
		t.Fatalf("copy did not write the clipboard (ok=%t mime=%q)", ok, mime)
	}
	report := string(content)
	for _, want := range []string{"Mirroring stopped", "Living Room · 192.168.1.24", raw} {
		if !strings.Contains(report, want) {
			t.Errorf("copied report lacks %q:\n%s", want, report)
		}
	}

	copyW := d.widthDp(measure, 12, font.SemiBold, "Copy details") + 52
	click(f32.Pt(left+float32(copyW)+8+20, buttonsY))
	if len(d.errors) != 0 {
		t.Fatalf("pop-ups after Dismiss = %d, want none", len(d.errors))
	}
}
