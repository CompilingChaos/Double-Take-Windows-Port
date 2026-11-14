//go:build windows || (linux && cgo && desktop)

package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"

	"doubletake/internal/daemon"
	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/io/pointer"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

func (d *desktop) layout(gtx layout.Context) {
	d.frameInterval = 0
	d.update(gtx)
	size := gtx.Constraints.Max
	if size.X == 0 || size.Y == 0 {
		return
	}
	if d.backgroundSize != size {
		d.background = paint.NewImageOp(backgroundImage(image.Pt(320, max(1, 320*size.Y/size.X))))
		d.backgroundSize = size
	}
	drawImage(gtx, d.background, image.Rectangle{Max: size})
	body := gtx
	if d.pending != nil {
		body = gtx.Disabled()
	}
	w, h := int(float32(size.X)/gtx.Metric.PxPerDp), int(float32(size.Y)/gtx.Metric.PxPerDp)
	sidebar, padding, mainPadding := 365, 24, 32
	if w <= 820 {
		sidebar, padding, mainPadding = 310, 18, 24
	}
	d.topbar(body, w)
	d.sidebar(body, sidebar, h, padding)
	d.mainPanel(body, sidebar, w, h, mainPadding)
	if d.pending != nil {
		d.pairingDialog(gtx, w, h, *d.pending)
	}
	// Error pop-ups sit in the bottom-right corner; brief notices stack above.
	d.toastStack(gtx, w, d.errorStack(gtx, w, h))
}

func (d *desktop) topbar(gtx layout.Context, w int) {
	box(gtx, rect(gtx, 0, 0, w, 76), 0, rgba(0x050a12, 184), color.NRGBA{})
	box(gtx, rect(gtx, 0, 75, w, 1), 0, line, color.NRGBA{})
	drawImage(gtx, d.icon, rect(gtx, 26, 14, 48, 48))
	d.label(gtx, rect(gtx, 86, 19, 240, 24), layout.W, 19, font.Bold, ink, "DoubleTake")
	d.label(gtx, rect(gtx, 86, 43, 240, 17), layout.W, 11, font.Normal, muted, "A I R P L A Y S T U D I O")
	service := d.tr("Discovery active", "Suche aktiv")
	for _, s := range d.state.Status.Streams {
		if s.State == daemon.StateStreaming {
			service = d.tr("AirPlay active", "AirPlay aktiv")
			break
		}
	}
	d.statusPill(gtx, dp(gtx, w-28), dp(gtx, 22), service, green, muted, rgba(0x0c192c, 158), line, false)
}

func (d *desktop) sidebar(gtx layout.Context, width, height, padding int) {
	box(gtx, rect(gtx, 0, 76, width, height-76), 0, rgba(0x050b14, 121), color.NRGBA{})
	box(gtx, rect(gtx, width-1, 76, 1, height-76), 0, line, color.NRGBA{})
	contentWidth := width - padding - 21
	d.label(gtx, rect(gtx, padding, 100, contentWidth, 18), layout.W, 11, font.Bold, blue,
		d.tr("N E A R B Y", "I N D E R N Ä H E"))
	d.label(gtx, rect(gtx, padding, 119, contentWidth, 32), layout.W, 22, font.SemiBold, ink, "Apple TVs")
	searchRect := rect(gtx, padding, 171, contentWidth, 45)
	d.editor(gtx, searchRect, 14, 42, &d.search, d.tr("Filter by name…", "Nach Namen filtern…"), rgba(0x111d30, 179))
	glyph(gtx, rect(gtx, padding+14, 184, 17, 17), "search", dim)
	if d.search.Len() > 0 {
		clearBounds := rect(gtx, padding+contentWidth-37, 178, 29, 29)
		background := color.NRGBA{}
		if d.clearSearch.Hovered() {
			background = rgba(0xffffff, 15)
		}
		d.button(gtx, clearBounds, &d.clearSearch, 9, background, color.NRGBA{}, color.NRGBA{}, muted, "×", "", 16)
	}
	query := strings.ToLower(strings.TrimSpace(d.search.Text()))
	visible := make([]daemon.DeviceInfo, 0, len(d.state.Devices))
	for _, device := range d.state.Devices {
		if strings.Contains(strings.ToLower(receiverName(device.Name)), query) {
			visible = append(visible, device)
		}
	}
	count := fmt.Sprintf(d.tr("%d receivers found", "%d Empfänger gefunden"), len(visible))
	if len(visible) == 1 {
		count = d.tr("1 receiver found", "1 Empfänger gefunden")
	}
	if query != "" {
		count = fmt.Sprintf(d.tr("%d matches", "%d Treffer"), len(visible))
		if len(visible) == 1 {
			count = d.tr("1 match", "1 Treffer")
		}
	}
	d.label(gtx, rect(gtx, padding+2, 231, contentWidth, 20), layout.W, 11, font.Normal, muted, count)
	if len(visible) == 0 {
		message := d.tr("No Apple TV matches that name.", "Kein Apple TV passt zu diesem Namen.")
		y := 285
		if query == "" {
			centre := (padding + contentWidth/2)
			d.radar(gtx, centre, y)
			message = d.tr("Searching for Apple TVs on your network…", "Apple TVs werden im Netzwerk gesucht…")
			y += 69
		}
		d.label(gtx, rect(gtx, padding, y, contentWidth, 36), layout.Center, 13, font.Normal, muted, message)
		return
	}
	at(gtx, dp(gtx, padding), dp(gtx, 260), func() {
		ctx := gtx
		ctx.Constraints = layout.Exact(image.Pt(dp(gtx, contentWidth-5), dp(gtx, max(0, height-278))))
		d.deviceList.Layout(ctx, len(visible), func(ctx layout.Context, i int) layout.Dimensions {
			device := visible[i]
			w := ctx.Constraints.Max.X
			r := image.Rect(0, 0, w, dp(ctx, 73))
			active := d.hasStream(device)
			k := receiverKey(device)
			b := d.devices[k]
			if b == nil {
				b = new(widget.Clickable)
				d.devices[k] = b
			}
			control := ctx
			if active {
				control = control.Disabled()
			}
			b.Layout(control, func(ctx layout.Context) layout.Dimensions {
				fill, border := rgba(0x0c1728, 168), line
				if b.Hovered() && !active {
					fill, border = rgba(0x11223b, 230), rgba(0x4c96ff, 107)
				}
				if active {
					gradient(ctx, r, 16, rgba(0x193c6f, 158), rgba(0x0b1b31, 204), rgba(0x4c96ff, 89))
				} else {
					box(ctx, r, 16, fill, border)
				}
				box(ctx, rect(ctx, 15, 15, 43, 43), 13, rgba(0x3173db, 33), rgba(0x589aff, 31))
				glyph(ctx, rect(ctx, 25, 25, 23, 23), "tv", blue)
				textW := max(0, w-dp(ctx, 106))
				d.label(ctx, image.Rect(dp(ctx, 70), dp(ctx, 15), dp(ctx, 70)+textW, dp(ctx, 40)), layout.W, 14, font.SemiBold, ink, receiverName(device.Name))
				model := device.Model
				if model == "" {
					model = d.tr("AirPlay receiver", "AirPlay-Empfänger")
				}
				d.label(ctx, image.Rect(dp(ctx, 70), dp(ctx, 39), dp(ctx, 70)+textW, dp(ctx, 58)), layout.W, 11, font.Normal, muted, model+" · "+device.IP)
				if active {
					pulse(ctx, f32.Pt(float32(w-dp(ctx, 25)), float32(r.Dy())/2), rgb(0x5bdeae))
				} else {
					glyph(ctx, image.Rect(w-dp(ctx, 34), dp(ctx, 27), w-dp(ctx, 16), dp(ctx, 45)), "chevron", rgb(0x526986))
					pointer.CursorPointer.Add(ctx.Ops)
				}
				semantic.LabelOp(receiverName(device.Name)).Add(ctx.Ops)
				return layout.Dimensions{Size: r.Size()}
			})
			return layout.Dimensions{Size: image.Pt(w, dp(ctx, 82))}
		})
	})
}

func (d *desktop) hasStream(device daemon.DeviceInfo) bool {
	for _, s := range d.state.Status.Streams {
		if s.DeviceIP == device.IP || (device.DeviceID != "" && strings.EqualFold(s.DeviceID, device.DeviceID)) {
			return true
		}
	}
	return false
}

func (d *desktop) editor(gtx layout.Context, r image.Rectangle, radius, left int, ed *widget.Editor, hint string, fill color.NRGBA) {
	border := line
	if gtx.Focused(ed) {
		box(gtx, r.Inset(-dp(gtx, 4)), radius+4, rgba(0x3d8dff, 23), color.NRGBA{})
		border = rgba(0x4994ff, 160)
	}
	box(gtx, r, radius, fill, border)
	at(gtx, r.Min.X+dp(gtx, left), r.Min.Y, func() {
		ctx := gtx
		ctx.Constraints = layout.Exact(image.Pt(max(0, r.Dx()-dp(ctx, left+15)), r.Dy()))
		layout.Center.Layout(ctx, func(ctx layout.Context) layout.Dimensions {
			ctx.Constraints.Min.X = ctx.Constraints.Max.X
			style := material.Editor(d.theme, ed, hint)
			style.TextSize, style.HintColor = 16, rgb(0x617491)
			semantic.LabelOp(hint).Add(ctx.Ops)
			return style.Layout(ctx)
		})
	})
}

func (d *desktop) radar(gtx layout.Context, cx, y int) {
	const outerRadius = 27
	d.animate(radarFrameRate)
	center := f32.Pt(float32(dp(gtx, cx)), (float32(y)+27.5)*gtx.Metric.PxPerDp)
	scale := gtx.Metric.PxPerDp * float32(breathe(gtx))
	defer op.Affine(f32.Affine2D{}.Scale(f32.Point{}, f32.Pt(scale, scale)).Offset(center)).Push(gtx.Ops).Pop()
	circle(gtx, f32.Point{}, outerRadius, 1, rgba(0x4e97ff, 75))
	circle(gtx, f32.Point{}, 17, 1, rgba(0x4e97ff, 53))

	angle := float32(float64(gtx.Now.UnixMilli()%2400)/2400*2*math.Pi - math.Pi/2)
	rotation := op.Affine(f32.Affine2D{}.Rotate(f32.Point{}, angle)).Push(gtx.Ops)
	drawImage(gtx, d.sweep, image.Rect(-outerRadius, -outerRadius, outerRadius, outerRadius))
	var beam clip.Path
	beam.Begin(gtx.Ops)
	beam.MoveTo(f32.Point{})
	beam.LineTo(f32.Pt(outerRadius, 0))
	paint.FillShape(gtx.Ops, rgba(0x67b2ff, 220), clip.Stroke{Path: beam.End(), Width: 1.15}.Op())
	rotation.Pop()

	circle(gtx, f32.Point{}, 7, 0, rgba(0x3d8dff, 18))
	circle(gtx, f32.Point{}, 2.5, 0, rgb(0x3d8dff))
}

func breathe(gtx layout.Context) float64 {
	return 1 + .07*(1-math.Cos(float64(gtx.Now.UnixMilli()%3000)/3000*2*math.Pi))/2
}

func (d *desktop) mainPanel(gtx layout.Context, sidebar, w, h, padding int) {
	width := w - sidebar - 2*padding
	if width < 1 {
		return
	}
	at(gtx, dp(gtx, sidebar+padding), dp(gtx, 76+padding), func() {
		ctx := gtx
		ctx.Constraints = layout.Exact(image.Pt(dp(gtx, width), dp(gtx, max(0, h-76-2*padding))))
		defer clip.Rect(image.Rectangle{Max: ctx.Constraints.Max}).Push(gtx.Ops).Pop()
		if len(d.state.Status.Streams) == 0 {
			d.welcome(ctx, width, max(0, h-76-2*padding))
			return
		}
		d.sessionList.Layout(ctx, len(d.state.Status.Streams)+1, func(ctx layout.Context, i int) layout.Dimensions {
			if i == 0 {
				return d.sessionHeader(ctx, width)
			}
			return d.sessionCard(ctx, width, d.state.Status.Streams[i-1])
		})
	})
}

func (d *desktop) welcome(gtx layout.Context, w, h int) {
	mid := w / 2
	top := h/2 - 109
	center := f32.Pt(float32(dp(gtx, mid)), float32(dp(gtx, top+65)))
	size := float32(dp(gtx, 146))
	scale := size / float32(d.glow.Size().X)
	halo := op.Affine(f32.Affine2D{}.Scale(f32.Point{}, f32.Pt(scale, scale)).
		Offset(center.Sub(f32.Pt(size/2, size/2)))).Push(gtx.Ops)
	d.glow.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	halo.Pop()
	glyph(gtx, rect(gtx, mid-27, top+36, 55, 55), "tv", rgb(0x7fc4ff))
	if d.globalFreeze {
		caption := d.tr("freezes or resumes from any app after you arm the control", "friert das Bild ein oder setzt es fort, nachdem du die Taste aktiviert hast")
		total := min(dp(gtx, w), d.width(gtx, 11, font.Normal, caption)+dp(gtx, 78))
		x := (dp(gtx, w) - total) / 2
		box(gtx, image.Rect(x, dp(gtx, 150+top), x+total, dp(gtx, 191+top)), 11, rgba(0x091322, 140), line)
		d.keycap(gtx, image.Rect(x+dp(gtx, 12), dp(gtx, 158+top), x+dp(gtx, 56), dp(gtx, 182+top)), "Enter")
		d.label(gtx, image.Rect(x+dp(gtx, 64), dp(gtx, 150+top), x+total-dp(gtx, 10), dp(gtx, 191+top)), layout.W, 11, font.Normal, muted, caption)
	} else {
		d.label(gtx, rect(gtx, 0, 150+top, w, 41), layout.Center, 14, font.Normal, ink,
			d.tr("Searching for Apple TVs on your network…", "Apple TVs werden im Netzwerk gesucht…"))
	}
}

func (d *desktop) keycap(gtx layout.Context, r image.Rectangle, value string) {
	box(gtx, r, 6, rgb(0x101f34), rgba(0x7ba6e1, 65))
	d.label(gtx, r, layout.Center, 11, font.Medium, rgb(0xcfe5ff), value)
}

func pulse(gtx layout.Context, center f32.Point, colour color.NRGBA) {
	scale := gtx.Metric.PxPerDp
	halo := colour
	halo.A = 20
	circle(gtx, center, 8.5*scale, 0, halo)
	circle(gtx, center, 3.5*scale, 0, colour)
}

func (d *desktop) statusPill(gtx layout.Context, right, y int, value string, dot, tint, fill, border color.NRGBA, waiting bool) {
	width := d.width(gtx, 12, font.Normal, value) + dp(gtx, 41)
	left := right - width
	r := image.Rect(left, y, right, y+dp(gtx, 33))
	box(gtx, r, 18, fill, border)
	center := f32.Pt(float32(left+dp(gtx, 16)), float32(r.Min.Y+r.Max.Y)/2)
	if waiting {
		// Shown only while a receiver is connecting or pairing.
		d.animate(spinnerFrameRate)
		spinner(gtx, int(center.X), int(math.Round(float64(center.Y))), dot)
	} else {
		pulse(gtx, center, dot)
	}
	d.label(gtx, image.Rect(left+dp(gtx, 28), y, right-dp(gtx, 10), r.Max.Y), layout.W, 12, font.Normal, tint, value)
}

func spinner(gtx layout.Context, x, y int, tint color.NRGBA) {
	r := dp(gtx, 4)
	faded := tint
	faded.A = 64
	box(gtx, image.Rect(x-r, y-r, x+r, y+r), 5, color.NRGBA{}, faded)
	angle := float64(gtx.Now.UnixMilli()%800) / 800 * 2 * math.Pi
	var path clip.Path
	path.Begin(gtx.Ops)
	for i := range 5 {
		a := angle + float64(i)*math.Pi/8
		point := f32.Pt(float32(float64(x)+math.Cos(a)*float64(r)), float32(float64(y)+math.Sin(a)*float64(r)))
		if i == 0 {
			path.MoveTo(point)
		} else {
			path.LineTo(point)
		}
	}
	paint.FillShape(gtx.Ops, tint, clip.Stroke{Path: path.End(), Width: float32(dp(gtx, 1))}.Op())
}

func (d *desktop) button(gtx layout.Context, r image.Rectangle, button *widget.Clickable, radius int, fill, end, border, fg color.NRGBA, label, icon string, size int) {
	at(gtx, r.Min.X, r.Min.Y, func() {
		ctx := gtx
		ctx.Constraints = layout.Exact(r.Size())
		button.Layout(ctx, func(ctx layout.Context) layout.Dimensions {
			surface := image.Rectangle{Max: r.Size()}
			if button.Hovered() && ctx.Enabled() {
				fill.R = min(fill.R, 239) + 16
				fill.G = min(fill.G, 239) + 16
				fill.B = min(fill.B, 239) + 16
				if end.A != 0 {
					end.R = min(end.R, 239) + 16
					end.G = min(end.G, 239) + 16
				}
			}
			if !ctx.Enabled() {
				fg.A /= 2
				fill.A /= 2
				end.A /= 2
			}
			if end.A != 0 {
				gradient(ctx, surface, radius, fill, end, border)
			} else {
				box(ctx, surface, radius, fill, border)
			}
			if ctx.Focused(button) {
				box(ctx, surface.Inset(dp(ctx, 2)), radius-2, color.NRGBA{}, blue)
			}
			if icon == "" {
				d.label(ctx, surface, layout.Center, size, font.SemiBold, fg, label)
			} else {
				textW := d.width(ctx, size, font.SemiBold, label)
				x := max(dp(ctx, 8), (r.Dx()-textW-dp(ctx, 27))/2)
				y := (r.Dy() - dp(ctx, 18)) / 2
				glyph(ctx, image.Rect(x, y, x+dp(ctx, 18), y+dp(ctx, 18)), icon, fg)
				d.label(ctx, image.Rect(x+dp(ctx, 27), 0, r.Dx()-dp(ctx, 6), r.Dy()), layout.W, size, font.SemiBold, fg, label)
			}
			if ctx.Enabled() {
				pointer.CursorPointer.Add(ctx.Ops)
			}
			semantic.LabelOp(label).Add(ctx.Ops)
			return layout.Dimensions{Size: r.Size()}
		})
	})
}
