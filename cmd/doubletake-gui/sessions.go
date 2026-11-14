//go:build windows || (linux && cgo && desktop)

package main

import (
	"fmt"
	"image"
	"image/color"
	"math"

	"doubletake/internal/daemon"
	"gioui.org/font"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/semantic"
	"gioui.org/layout"
)

func (d *desktop) sessionHeader(gtx layout.Context, w int) layout.Dimensions {
	y := 0
	d.label(gtx, rect(gtx, 0, y, w, 17), layout.W, 11, font.Bold, blue,
		d.tr("N O W  P L A Y I N G", "A K T U E L L E  Ü B E R T R A G U N G"))
	d.label(gtx, rect(gtx, 0, y+17, w, 36), layout.W, 28, font.Bold, ink,
		d.tr("Active sessions", "Aktive Verbindungen"))
	connected := fmt.Sprintf(d.tr("%d connected", "%d verbunden"), len(d.state.Status.Streams))
	d.statusPill(gtx, dp(gtx, w), dp(gtx, y+27), connected, green, rgb(0x8eb1da), color.NRGBA{}, color.NRGBA{}, false)
	y += 77
	if d.state.FreezeArmedTarget != "" {
		message := d.tr("Press Enter in any app to freeze. Press Esc here to cancel.", "Drücke in einer beliebigen App die Eingabetaste, um das Bild einzufrieren. Mit Esc hier abbrechen.")
		if !d.globalFreeze {
			message = d.tr("Press Enter in DoubleTake to freeze. Press Esc to cancel.", "Drücke in DoubleTake die Eingabetaste, um das Bild einzufrieren. Mit Esc abbrechen.")
		}
		if !d.state.FreezeArmedAction {
			message = d.tr("Press Enter in any app to resume live video. Press Esc here to cancel.", "Drücke in einer beliebigen App die Eingabetaste, um das Livebild fortzusetzen. Mit Esc hier abbrechen.")
			if !d.globalFreeze {
				message = d.tr("Press Enter in DoubleTake to resume. Press Esc to cancel.", "Drücke in DoubleTake die Eingabetaste, um das Livebild fortzusetzen. Mit Esc abbrechen.")
			}
		}
		box(gtx, rect(gtx, 0, y, w, 51), 14, rgba(0x19718b, 26), rgba(0x61e6ff, 46))
		d.label(gtx, rect(gtx, 16, y, max(0, w-120), 51), layout.W, 12, font.Normal, rgb(0xa5e9f7), message)
		d.keycap(gtx, rect(gtx, w-105, y+13, 45, 25), "Enter")
		d.keycap(gtx, rect(gtx, w-50, y+13, 34, 25), "Esc")
		y += 67
	}
	return layout.Dimensions{Size: image.Pt(dp(gtx, w), dp(gtx, y))}
}

func (d *desktop) sessionCard(gtx layout.Context, w int, s daemon.StreamInfo) layout.Dimensions {
	controls := d.sessions[s.DeviceIP]
	if controls == nil {
		controls = new(streamControls)
		d.sessions[s.DeviceIP] = controls
	}
	height := 231
	if s.HasAudio {
		height += 83
	}
	gradient(gtx, rect(gtx, 0, 0, w, height), 24, rgba(0x0f1f36, 235), rgba(0x081220, 235), rgba(0x5b97e5, 46))
	gradient(gtx, rect(gtx, 26, 26, 54, 54), 17, rgba(0x2868cd, 64), rgba(0x183663, 38), rgba(0x63a9ff, 51))
	glyph(gtx, rect(gtx, 39, 39, 28, 28), "tv", rgb(0x84c6ff))

	ready := s.State == daemon.StateStreaming
	state, dot, text, fill, border := d.tr("Live", "Live"), green, rgb(0x7de0ba), rgba(0x207055, 26), rgba(0x54dca2, 41)
	if s.Frozen {
		state, dot, text, fill, border = d.tr("Frozen", "Eingefroren"), rgb(0x61e6ff), rgb(0x94dfff), rgba(0x1e8099, 28), rgba(0x61e6ff, 51)
	} else if !ready {
		state = d.tr("Connecting", "Verbindung wird hergestellt")
		if s.State == daemon.StatePINRequired {
			state = d.tr("Pairing", "Kopplung")
		}
		dot, text, fill, border = rgb(0x82bbff), rgb(0x82bbff), rgba(0x265fb8, 26), rgba(0x5299ff, 49)
	}
	right := w - 26
	chipW := d.width(gtx, 12, font.Normal, state) + dp(gtx, 41)
	d.statusPill(gtx, dp(gtx, right), dp(gtx, 37), state, dot, text, fill, border, !ready)
	nameW := dp(gtx, right-110) - chipW
	d.label(gtx, image.Rect(dp(gtx, 95), dp(gtx, 26), dp(gtx, 95)+nameW, dp(gtx, 55)), layout.W, 19, font.SemiBold, ink, receiverName(s.Device))
	d.label(gtx, rect(gtx, 95, 56, w-130, 21), layout.W, 12, font.Normal, muted, s.DeviceIP)

	video := d.tr("Live screen", "Livebild")
	if s.Frozen {
		video = d.tr("Held frame", "Standbild")
	}
	audio := d.tr("Off", "Aus")
	if s.HasAudio {
		audio = d.tr("Streaming", "Übertragung")
		if s.AudioMuted {
			audio = d.tr("Muted", "Stumm")
		}
	}
	x, y := dp(gtx, 26), 101
	for _, metric := range []struct{ label, value string }{
		{"Video", video}, {"Audio", audio}, {"AirPlay", d.tr("Secure", "Sicher")},
	} {
		labelWidth := d.width(gtx, 11, font.SemiBold, metric.label)
		valueWidth := d.width(gtx, 11, font.Normal, " · "+metric.value)
		width := labelWidth + valueWidth + dp(gtx, 24)
		if x+width > dp(gtx, right) && x > dp(gtx, 26) {
			x, y = dp(gtx, 26), y+38
		}
		box(gtx, image.Rect(x, dp(gtx, y), x+width, dp(gtx, y+33)), 10, rgba(0x030b15, 87), line)
		d.label(gtx, image.Rect(x+dp(gtx, 11), dp(gtx, y), x+dp(gtx, 11)+labelWidth, dp(gtx, y+33)), layout.W, 11, font.SemiBold, rgb(0xcbdcf2), metric.label)
		d.label(gtx, image.Rect(x+dp(gtx, 11)+labelWidth, dp(gtx, y), x+width-dp(gtx, 7), dp(gtx, y+33)), layout.W, 11, font.Normal, muted, " · "+metric.value)
		x += width + dp(gtx, 9)
	}

	buttonY := height - 74
	if d.busy[s.DeviceIP] {
		gtx = gtx.Disabled()
	}
	if s.HasAudio {
		d.audioPanel(gtx, w, buttonY-88, s, controls)
	}
	half := (w - 52 - 11) / 2
	freezeCtx := gtx
	if !ready {
		freezeCtx = gtx.Disabled()
	}
	freezeLabel, freezeIcon := d.tr("Freeze", "Einfrieren"), "freeze"
	if s.Frozen {
		freezeLabel, freezeIcon = d.tr("Unfreeze", "Fortsetzen"), "play"
	}
	start, end := rgb(0x276fe5), rgb(0x3da8ff)
	if d.state.FreezeArmedTarget == s.DeviceIP {
		freezeLabel = d.tr("Press Enter…", "Eingabetaste drücken…")
		start, end = rgb(0x167794), rgb(0x27b4d2)
	}
	d.button(freezeCtx, rect(gtx, 26, buttonY, half, 48), &controls.freeze, 14, start, end, rgba(0xffffff, 38), ink, freezeLabel, freezeIcon, 13)
	d.button(gtx, rect(gtx, 26+half+11, buttonY, w-52-half-11, 48), &controls.disconnect, 14, rgba(0x62192b, 31), color.NRGBA{}, rgba(0xff5c7a, 46), rgb(0xff91a6), d.tr("Disconnect", "Trennen"), "disconnect", 13)
	return layout.Dimensions{Size: image.Pt(dp(gtx, w), dp(gtx, height+18))}
}

func (d *desktop) audioPanel(gtx layout.Context, w, y int, s daemon.StreamInfo, controls *streamControls) {
	box(gtx, rect(gtx, 26, y, w-52, 69), 14, rgba(0x071222, 140), rgba(0x5797eb, 46))
	label, icon, caption := d.tr("Mute", "Stumm"), "sound", d.tr("Stream volume", "Lautstärke")
	if s.AudioMuted {
		label, icon, caption = d.tr("Unmute", "Ton an"), "mute", d.tr("Audio muted", "Ton stummgeschaltet")
	}
	d.button(gtx, rect(gtx, 41, y+16, 91, 37), &controls.mute, 11, rgba(0x2d70cc, 31), color.NRGBA{}, rgba(0x70aeff, 51), rgb(0x9accff), label, icon, 12)
	if !controls.volume.Dragging() && !controls.volumeEdited && !d.busy[s.DeviceIP] {
		controls.volume.Value = float32(min(100, max(0, s.AudioVolume))) / 100
	}
	x, width := 146, w-187
	if width <= 13 {
		return
	}
	d.label(gtx, rect(gtx, x, y+11, width, 21), layout.W, 11, font.Normal, rgb(0xb6cce8), caption)
	d.label(gtx, rect(gtx, x, y+11, width, 21), layout.E, 12, font.SemiBold, rgb(0xe2efff), fmt.Sprintf("%d%%", int(math.Round(float64(controls.volume.Value*100)))))
	left, right, cy := dp(gtx, x+6), dp(gtx, x+width-6), dp(gtx, y+43)
	box(gtx, image.Rect(left, cy-dp(gtx, 2), right, cy+dp(gtx, 3)), 3, rgb(0x354358), color.NRGBA{})
	thumb := left + int(controls.volume.Value*float32(right-left))
	box(gtx, image.Rect(left, cy-dp(gtx, 2), thumb, cy+dp(gtx, 3)), 3, rgb(0x58a6ff), color.NRGBA{})
	box(gtx, image.Rect(thumb-dp(gtx, 7), cy-dp(gtx, 7), thumb+dp(gtx, 7), cy+dp(gtx, 7)), 7, rgb(0x58a6ff), color.NRGBA{})
	at(gtx, left, cy-dp(gtx, 10), func() {
		ctx := gtx
		ctx.Constraints = layout.Exact(image.Pt(right-left, dp(gtx, 20)))
		pointer.CursorPointer.Add(ctx.Ops)
		semantic.LabelOp(caption).Add(ctx.Ops)
		controls.volume.Layout(ctx, layout.Horizontal, 6)
	})
}

func (d *desktop) pairingDialog(gtx layout.Context, w, h int, pending daemon.StreamInfo) {
	box(gtx, rect(gtx, 0, 0, w, h), 0, rgba(0x02060c, 179), color.NRGBA{})
	width := min(430, w-44)
	x, y := (w-width)/2, (h-312)/2
	gradient(gtx, rect(gtx, x, y, width, 312), 24, rgb(0x111f34), rgb(0x091421), rgba(0x67a3ef, 51))
	box(gtx, rect(gtx, x+27, y+27, 45, 45), 14, rgba(0x3079e6, 36), rgba(0x5a9eff, 36))
	glyph(gtx, rect(gtx, x+39, y+39, 22, 22), "lock", rgb(0x7ec2ff))
	title, placeholder := d.tr("Enter pairing code", "Kopplungscode eingeben"), d.tr("Pairing code", "Kopplungscode")
	message := fmt.Sprintf(d.tr("Type the code shown on %s.", "Gib den Code ein, der auf %s angezeigt wird."), receiverName(pending.Device))
	if pending.CredentialKind == daemon.CredentialKindPassword {
		title, placeholder = d.tr("Receiver password", "Empfängerpasswort"), d.tr("Password", "Passwort")
		message = fmt.Sprintf(d.tr("Enter the password configured on %s.", "Gib das Passwort für %s ein."), receiverName(pending.Device))
	}
	d.label(gtx, rect(gtx, x+27, y+90, width-54, 30), layout.W, 22, font.Bold, ink, title)
	d.label(gtx, rect(gtx, x+27, y+128, width-54, 21), layout.W, 13, font.Normal, muted, message)
	if d.focusCredential {
		gtx.Execute(key.FocusCmd{Tag: &d.credential})
		d.focusCredential = false
	}
	d.editor(gtx, rect(gtx, x+27, y+167, width-54, 47), 13, 14, &d.credential, placeholder, rgb(0x081321))
	half := (width - 64) / 2
	d.button(gtx, rect(gtx, x+27, y+237, half, 48), &d.cancel, 14, rgba(0xffffff, 9), color.NRGBA{}, line, ink, d.tr("Cancel", "Abbrechen"), "", 13)
	d.button(gtx, rect(gtx, x+37+half, y+237, width-64-half, 48), &d.submit, 14, rgb(0x276fe5), rgb(0x3da8ff), color.NRGBA{}, ink, d.tr("Continue", "Weiter"), "", 13)
}

// toastStack draws brief notices upwards from bottom (dp). Errors are shown
// as pop-ups instead; see errorStack.
func (d *desktop) toastStack(gtx layout.Context, w, bottom int) {
	y := bottom
	width := min(360, w-42)
	current := d.toasts[:0]
	for _, t := range d.toasts {
		if gtx.Now.Before(t.expires) {
			current = append(current, t)
		}
	}
	d.toasts = current
	for i := len(d.toasts) - 1; i >= 0; i-- {
		t := d.toasts[i]
		border, tint := rgba(0x72a4e5, 46), rgb(0xdbeaff)
		y -= 47
		x := w - 21 - width
		box(gtx, rect(gtx, x, y, width, 47), 14, rgba(0x0d1a2c, 245), border)
		d.label(gtx, rect(gtx, x+15, y, width-30, 47), layout.W, 12, font.Normal, tint, t.message)
		y -= 9
	}
}
