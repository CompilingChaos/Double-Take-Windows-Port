//go:build windows || (linux && cgo && desktop)

package main

import (
	"gioui.org/app"
	"gioui.org/op"
)

func runWindow(b *backend) error {
	w := new(app.Window)
	w.Option(app.Title("DoubleTake"), app.Size(1120, 760), app.MinSize(920, 640))
	configureWindow(w)
	ui := newDesktop(b, uiLocale(), b.hook != nil)
	ui.invalidate = w.Invalidate
	var ops op.Ops
	for {
		switch e := w.Event().(type) {
		case app.ViewEvent:
			configureView(e)
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			ui.layout(gtx)
			gtx.Execute(op.InvalidateCmd{At: ui.nextFrame(gtx.Now)})
			e.Frame(gtx.Ops)
		case app.DestroyEvent:
			return e.Err
		}
	}
}
