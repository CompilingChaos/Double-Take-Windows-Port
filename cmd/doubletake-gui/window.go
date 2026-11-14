//go:build windows || (linux && cgo && desktop)

package main

import (
	"time"

	"gioui.org/app"
	"gioui.org/op"
)

func runWindow(b *backend) error {
	w := new(app.Window)
	w.Option(app.Title("DoubleTake"), app.Size(1120, 760), app.MinSize(920, 640))
	configureWindow(w)
	ui := newDesktop(b, uiLocale(), b.hook != nil)
	var ops op.Ops
	for {
		switch e := w.Event().(type) {
		case app.ViewEvent:
			configureView(e)
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			ui.layout(gtx)
			interval := 33 * time.Millisecond
			if len(ui.state.Devices) == 0 || len(ui.state.Status.Streams) == 0 {
				interval = time.Second / 60
			}
			gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(interval)})
			e.Frame(gtx.Ops)
		case app.DestroyEvent:
			return e.Err
		}
	}
}
