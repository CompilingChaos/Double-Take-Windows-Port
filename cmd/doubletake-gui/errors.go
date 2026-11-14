//go:build windows || (linux && cgo && desktop)

package main

import (
	"fmt"
	"image"
	"image/color"
	"io"
	"net"
	"strings"
	"time"

	"gioui.org/font"
	"gioui.org/io/clipboard"
	"gioui.org/io/event"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

const (
	// maxErrorPopups bounds how many unread errors stay on screen; the oldest
	// gives way to a new one.
	maxErrorPopups = 4
	copiedFeedback = 2 * time.Second
	monoTypeface   = "Consolas, Go Mono, monospace"
)

// errorPopup is one error shown until the user dismisses it. It pairs a
// plain-language summary with the technical message, which can be copied.
type errorPopup struct {
	raw, title, explanation string
	receiver                string
	at                      time.Time
	count                   int
	copy, dismiss, close    widget.Clickable
	copiedUntil             time.Time
}

// raiseError shows raw as an error pop-up. A repeat of an error already on
// screen counts up on that pop-up instead of adding another.
func (d *desktop) raiseError(now time.Time, raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return
	}
	for i, existing := range d.errors {
		if existing.raw == raw {
			existing.count++
			existing.at = now
			d.errors = append(append(d.errors[:i:i], d.errors[i+1:]...), existing)
			return
		}
	}
	title, explanation := explainError(raw, d.german)
	d.errors = append(d.errors, &errorPopup{
		raw: raw, title: title, explanation: explanation,
		receiver: d.receiverForError(raw), at: now, count: 1,
	})
	if len(d.errors) > maxErrorPopups {
		d.errors = d.errors[len(d.errors)-maxErrorPopups:]
	}
}

// receiverForError names the receiver a daemon error is about. Daemon errors
// about one receiver start with its address, as in "192.0.2.1: ...".
func (d *desktop) receiverForError(raw string) string {
	address, _, ok := strings.Cut(raw, ": ")
	if !ok || net.ParseIP(address) == nil {
		return ""
	}
	for _, device := range d.state.Devices {
		if device.IP == address && device.Name != "" {
			return device.Name + " · " + address
		}
	}
	for _, stream := range d.state.Status.Streams {
		if stream.DeviceIP == address && stream.Device != "" {
			return stream.Device + " · " + address
		}
	}
	return address
}

// updateErrors handles the pop-up buttons. Copy puts a self-contained report
// on the clipboard, suitable for pasting into a bug report or a message.
func (d *desktop) updateErrors(gtx layout.Context) {
	kept := d.errors[:0]
	for _, popup := range d.errors {
		if popup.copy.Clicked(gtx) {
			gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(d.errorReport(popup)))})
			popup.copiedUntil = gtx.Now.Add(copiedFeedback)
		}
		if popup.dismiss.Clicked(gtx) || popup.close.Clicked(gtx) {
			continue
		}
		kept = append(kept, popup)
	}
	clear(d.errors[len(kept):])
	d.errors = kept
}

// errorReport is the text placed on the clipboard for one error.
func (d *desktop) errorReport(popup *errorPopup) string {
	var b strings.Builder
	fmt.Fprintf(&b, "DoubleTake: %s\n", popup.title)
	fmt.Fprintf(&b, "%s\n", popup.explanation)
	if popup.receiver != "" {
		fmt.Fprintf(&b, "%s %s\n", d.tr("Receiver:", "Empfänger:"), popup.receiver)
	}
	fmt.Fprintf(&b, "%s %s", d.tr("Time:", "Zeit:"), popup.at.Format("2006-01-02 15:04:05"))
	if popup.count > 1 {
		fmt.Fprintf(&b, d.tr(" (happened %d times)", " (%d-mal aufgetreten)"), popup.count)
	}
	fmt.Fprintf(&b, "\n%s %s\n", d.tr("Details:", "Details:"), popup.raw)
	return b.String()
}

// errorStack draws the pop-ups in the bottom-right corner, newest lowest,
// and returns the top edge of the stack in dp.
func (d *desktop) errorStack(gtx layout.Context, w, h int) int {
	bottom := h - 21
	width := min(420, w-42)
	if width <= 120 {
		return bottom
	}
	for i := len(d.errors) - 1; i >= 0; i-- {
		popup := d.errors[i]
		height := d.errorPopupHeight(gtx, popup, width)
		top := bottom - height
		d.errorPopupCard(gtx, popup, w-21-width, top, width, height)
		bottom = top - 10
	}
	return bottom
}

// Pop-up geometry, in dp.
const (
	popupPad     = 18
	popupTextX   = 62
	popupHeader  = 22
	popupButtonH = 32
)

func (d *desktop) errorPopupHeight(gtx layout.Context, popup *errorPopup, width int) int {
	textW := width - popupTextX - popupPad
	height := popupPad + popupHeader
	if popup.receiver != "" {
		height += 18
	}
	height += 6 + d.textHeight(gtx, textW, 12, font.Normal, "", popup.explanation, 3)
	height += 12 + 2*10 + d.textHeight(gtx, width-2*popupPad-20, 11, font.Normal, monoTypeface, popup.raw, 3)
	return height + 14 + popupButtonH + popupPad
}

func (d *desktop) errorPopupCard(gtx layout.Context, popup *errorPopup, x, y, width, height int) {
	card := rect(gtx, x, y, width, height)
	// Claim the card's area so a click on it never reaches what lies below.
	area := clip.Rect(card).Push(gtx.Ops)
	event.Op(gtx.Ops, popup)
	area.Pop()

	popupShadow(gtx, card, 16)
	box(gtx, card, 16, rgba(0x111a2c, 250), rgba(0xff6b85, 96))
	box(gtx, rect(gtx, x, y+18, 3, height-36), 2, rgb(0xff6b85), color.NRGBA{})

	// Header: alert badge, title and close button.
	badge := rect(gtx, x+popupPad, y+popupPad-2, 30, 30)
	paint.FillShape(gtx.Ops, rgba(0xff6b85, 38), clip.Ellipse(badge).Op(gtx.Ops))
	glyph(gtx, rect(gtx, x+popupPad+6, y+popupPad+4, 18, 18), "alert", rgb(0xff8fa3))
	textX, textW := x+popupTextX, width-popupTextX-popupPad
	d.label(gtx, rect(gtx, textX, y+popupPad, textW-30, popupHeader), layout.W, 15, font.SemiBold, ink, popup.title)
	d.button(gtx, rect(gtx, x+width-popupPad-24, y+popupPad-3, 28, 28), &popup.close, 8,
		color.NRGBA{}, color.NRGBA{}, color.NRGBA{}, muted, "×", "", 17)

	cy := y + popupPad + popupHeader
	if popup.receiver != "" {
		d.label(gtx, rect(gtx, textX, cy, textW, 18), layout.W, 11, font.Normal, dim, popup.receiver)
		cy += 18
	}
	cy += 6
	cy += d.paragraph(gtx, textX, cy, textW, 12, font.Normal, "", rgb(0xd3deee), popup.explanation, 3)

	// Technical details in a monospace well, as they will be copied.
	cy += 12
	detailsH := d.textHeight(gtx, width-2*popupPad-20, 11, font.Normal, monoTypeface, popup.raw, 3) + 2*10
	box(gtx, rect(gtx, x+popupPad, cy, width-2*popupPad, detailsH), 10, rgba(0x02060c, 160), line)
	d.paragraph(gtx, x+popupPad+10, cy+10, width-2*popupPad-20, 11, font.Normal, monoTypeface, rgb(0x9fb2cc), popup.raw, 3)
	cy += detailsH + 14

	copyLabel, copyIcon := d.tr("Copy details", "Details kopieren"), "copy"
	if gtx.Now.Before(popup.copiedUntil) {
		copyLabel, copyIcon = d.tr("Copied", "Kopiert"), "check"
	}
	copyW := d.widthDp(gtx, 12, font.SemiBold, d.tr("Copy details", "Details kopieren")) + 52
	d.button(gtx, rect(gtx, x+popupPad, cy, copyW, popupButtonH), &popup.copy, 10,
		rgba(0x2d70cc, 40), color.NRGBA{}, rgba(0x70aeff, 80), rgb(0xb4d6ff), copyLabel, copyIcon, 12)
	dismissLabel := d.tr("Dismiss", "Schließen")
	dismissW := d.widthDp(gtx, 12, font.SemiBold, dismissLabel) + 30
	d.button(gtx, rect(gtx, x+popupPad+copyW+8, cy, dismissW, popupButtonH), &popup.dismiss, 10,
		rgba(0xffffff, 10), color.NRGBA{}, line, rgb(0xc9d6e8), dismissLabel, "", 12)
	if popup.count > 1 {
		repeat := fmt.Sprintf(d.tr("Happened %d times", "%d-mal aufgetreten"), popup.count)
		left := x + popupPad + copyW + 8 + dismissW + 8
		d.label(gtx, rect(gtx, left, cy, x+width-popupPad-left, popupButtonH), layout.E, 11, font.Normal, dim, repeat)
	}
	semantic.LabelOp(popup.title + ". " + popup.explanation).Add(gtx.Ops)
}

// popupShadow darkens the area just around a floating card.
func popupShadow(gtx layout.Context, bounds image.Rectangle, radius int) {
	const layers = 8
	reach := dp(gtx, 14)
	for i := layers; i >= 1; i-- {
		grow := reach * i / layers
		r := bounds.Add(image.Pt(0, reach/3)).Inset(-grow)
		paint.FillShape(gtx.Ops, color.NRGBA{A: 12}, clip.UniformRRect(r, dp(gtx, radius)+grow).Op(gtx.Ops))
	}
}

// widthDp measures single-line text in dp.
func (d *desktop) widthDp(gtx layout.Context, size int, weight font.Weight, value string) int {
	return int(float32(d.width(gtx, size, weight, value))/gtx.Metric.PxPerDp + .5)
}

// paragraph draws wrapped text at (x, y) within width dp and returns the
// height it used in dp.
func (d *desktop) paragraph(gtx layout.Context, x, y, width, size int, weight font.Weight, typeface string, fg color.NRGBA, value string, maxLines int) int {
	var dims layout.Dimensions
	at(gtx, dp(gtx, x), dp(gtx, y), func() {
		dims = d.textLayout(gtx, width, size, weight, typeface, fg, value, maxLines)
	})
	return int(float32(dims.Size.Y)/gtx.Metric.PxPerDp + .5)
}

// textHeight measures wrapped text without drawing it, in dp.
func (d *desktop) textHeight(gtx layout.Context, width, size int, weight font.Weight, typeface, value string, maxLines int) int {
	macro := op.Record(gtx.Ops)
	dims := d.textLayout(gtx, width, size, weight, typeface, ink, value, maxLines)
	macro.Stop()
	return int(float32(dims.Size.Y)/gtx.Metric.PxPerDp + .5)
}

func (d *desktop) textLayout(gtx layout.Context, width, size int, weight font.Weight, typeface string, fg color.NRGBA, value string, maxLines int) layout.Dimensions {
	ctx := gtx
	ctx.Constraints = layout.Constraints{Max: image.Pt(dp(gtx, width), dp(gtx, 400))}
	label := material.Label(d.theme, unit.Sp(size), value)
	label.Color, label.Font.Weight, label.MaxLines = fg, weight, maxLines
	if typeface != "" {
		label.Font.Typeface = font.Typeface(typeface)
	}
	return label.Layout(ctx)
}

// errorExplanation maps a family of technical errors to a short title and a
// sentence saying what happened and what to do, in English and German.
type errorExplanation struct {
	match                func(string) bool
	titleEN, titleDE     string
	explainEN, explainDE string
}

func containsAny(keywords ...string) func(string) bool {
	return func(message string) bool {
		for _, keyword := range keywords {
			if strings.Contains(message, keyword) {
				return true
			}
		}
		return false
	}
}

// Order matters: the first match wins, so specific causes come before the
// general ones that their messages also contain.
var errorExplanations = []errorExplanation{
	{containsAny("mirroring stopped"),
		"Mirroring stopped", "Spiegelung beendet",
		"The connection to the receiver was lost. It may have been switched off, gone to sleep or left the network.",
		"Die Verbindung zum Empfänger ist abgebrochen. Er wurde vielleicht ausgeschaltet, ist im Ruhezustand oder nicht mehr im Netzwerk."},
	{containsAny("capture failed", "capture error", "capture exited"),
		"Screen capture stopped", "Bildschirmaufnahme beendet",
		"DoubleTake could no longer record your screen, so mirroring to the affected receivers ended. Try connecting again.",
		"DoubleTake konnte den Bildschirm nicht mehr aufnehmen, daher wurde die Spiegelung beendet. Verbinde dich erneut."},
	{containsAny("ffmpeg"),
		"FFmpeg is missing", "FFmpeg fehlt",
		"DoubleTake needs FFmpeg to record your screen. Install it, for example with winget install Gyan.FFmpeg, then restart DoubleTake.",
		"DoubleTake braucht FFmpeg für die Bildschirmaufnahme. Installiere es, zum Beispiel mit winget install Gyan.FFmpeg, und starte DoubleTake neu."},
	{containsAny("gst-launch", "gst-inspect", "gstreamer"),
		"GStreamer is missing", "GStreamer fehlt",
		"DoubleTake needs GStreamer to record your screen. Install gstreamer1.0-tools and its plugins, then restart DoubleTake.",
		"DoubleTake braucht GStreamer für die Bildschirmaufnahme. Installiere gstreamer1.0-tools und die Plugins und starte DoubleTake neu."},
	{containsAny("global enter", "keyboard hook", "xinput"),
		"Freeze shortcut limited", "Einfrier-Taste eingeschränkt",
		"Enter can freeze the picture only while DoubleTake is the active window. Everything else works normally.",
		"Die Eingabetaste friert das Bild nur ein, solange DoubleTake das aktive Fenster ist. Alles andere funktioniert normal."},
	{containsAny("background service"),
		"Background service stopped", "Hintergrunddienst beendet",
		"The part of DoubleTake that talks to receivers stopped. Restart DoubleTake to continue.",
		"Der Teil von DoubleTake, der mit den Empfängern spricht, wurde beendet. Starte DoubleTake neu."},
	{containsAny("refusing to cast", "did not prove", "named itself"),
		"Receiver identity check failed", "Empfänger nicht bestätigt",
		"This receiver did not match the one this PC paired with, so DoubleTake stopped for safety. If you reset or replaced it, remove its saved pairing and connect again.",
		"Der Empfänger passt nicht zu dem, mit dem dieser PC gekoppelt ist, daher wurde zur Sicherheit abgebrochen. Wurde er zurückgesetzt oder ersetzt, entferne die gespeicherte Kopplung und verbinde dich erneut."},
	{containsAny("connect to", "connection refused", "no route", "unreachable", "i/o timeout", "actively refused", "connectex"),
		"Couldn't reach the receiver", "Empfänger nicht erreichbar",
		"Make sure it is switched on and on the same network as this PC, then try again.",
		"Prüfe, ob er eingeschaltet und im selben Netzwerk wie dieser PC ist, und versuche es erneut."},
	{containsAny("pin pairing", "password pairing", "pairing", "pair-verify", "pair-setup", "wait for pin"),
		"Pairing failed", "Kopplung fehlgeschlagen",
		"The code may have been mistyped or has expired. Connect again to get a new code.",
		"Der Code war vielleicht falsch oder ist abgelaufen. Verbinde dich erneut, um einen neuen Code zu erhalten."},
	{containsAny("password", "digest", "401", "unauthorized"),
		"Password not accepted", "Passwort nicht akzeptiert",
		"The receiver rejected the password. Check the password set on the receiver and try again.",
		"Der Empfänger hat das Passwort abgelehnt. Prüfe das am Empfänger eingestellte Passwort und versuche es erneut."},
	{containsAny("mirror setup failed", "fairplay", "setup"),
		"The receiver declined the stream", "Empfänger hat abgelehnt",
		"It may be busy with another device or not support this kind of mirroring. Try again in a moment.",
		"Er ist vielleicht mit einem anderen Gerät beschäftigt oder unterstützt diese Art der Spiegelung nicht. Versuche es gleich noch einmal."},
	{containsAny("audio"),
		"Audio problem", "Tonproblem",
		"The picture keeps mirroring, but receiver audio could not be started or changed.",
		"Das Bild wird weiter gespiegelt, aber der Ton konnte nicht gestartet oder geändert werden."},
	{containsAny("already connected"),
		"Already connected", "Bereits verbunden",
		"DoubleTake is already mirroring to this receiver or still connecting to it.",
		"DoubleTake spiegelt bereits auf diesen Empfänger oder verbindet sich noch."},
	{containsAny("no available devices", "no device"),
		"No receiver available", "Kein Empfänger verfügbar",
		"There is no receiver free to connect to. Wait for one to appear in the list.",
		"Es ist kein freier Empfänger verfügbar. Warte, bis einer in der Liste erscheint."},
	{containsAny("not ready", "not currently streaming", "no active stream"),
		"Not ready yet", "Noch nicht bereit",
		"Wait until the stream is live, then try again.",
		"Warte, bis die Übertragung läuft, und versuche es dann erneut."},
}

// explainError returns the title and explanation for a technical message.
func explainError(raw string, german bool) (title, explanation string) {
	message := strings.ToLower(raw)
	for _, entry := range errorExplanations {
		if entry.match(message) {
			if german {
				return entry.titleDE, entry.explainDE
			}
			return entry.titleEN, entry.explainEN
		}
	}
	if german {
		return "Etwas ist schiefgelaufen", "DoubleTake konnte diese Aktion nicht abschließen. Die Details unten helfen bei der Fehlersuche."
	}
	return "Something went wrong", "DoubleTake couldn't finish that action. The details below help with troubleshooting."
}
