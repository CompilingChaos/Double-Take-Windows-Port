//go:build windows || (linux && cgo && desktop)

package main

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math"
	"strings"
	"time"

	"doubletake/internal/daemon"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op/paint"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

type desktopBackend interface {
	state() snapshot
	connect(string, int, string) daemon.Response
	submitCredential(string, string) daemon.Response
	disconnect(string) daemon.Response
	armFreeze(string, bool) error
	disarmFreeze(string)
	focusedEnter()
	setMuted(string, bool) daemon.Response
	setVolume(string, int) daemon.Response
}

type desktop struct {
	backend                     desktopBackend
	theme                       *material.Theme
	german, globalFreeze        bool
	icon, glow, sweep           paint.ImageOp
	background                  paint.ImageOp
	backgroundSize              image.Point
	state                       snapshot
	nextPoll                    time.Time
	deviceList, sessionList     layout.List
	search, credential          widget.Editor
	clearSearch, cancel, submit widget.Clickable
	devices                     map[deviceKey]*widget.Clickable
	sessions                    map[string]*streamControls
	pending                     *daemon.StreamInfo
	focusCredential             bool
	toasts                      []toast
	lastError                   string
	results                     chan commandResult
	busy                        map[string]bool
	// frameInterval is the redraw rate the animations drawn in the current
	// frame need; zero when the frame is static.
	frameInterval time.Duration
	// invalidate requests a frame from another goroutine. It may be nil.
	invalidate func()
}

const (
	statePollInterval = 850 * time.Millisecond
	radarFrameRate    = time.Second / 60
	spinnerFrameRate  = time.Second / 15
)

// animate asks for the next frame within interval. Only animations on screen
// call it, so a static window is redrawn just for input and state polls.
func (d *desktop) animate(interval time.Duration) {
	if d.frameInterval == 0 || interval < d.frameInterval {
		d.frameInterval = interval
	}
}

// nextFrame returns when the window must be redrawn without new input: at the
// next animation step, or else at the next daemon state poll.
func (d *desktop) nextFrame(now time.Time) time.Time {
	next := d.nextPoll
	if d.frameInterval > 0 {
		if step := now.Add(d.frameInterval); step.Before(next) {
			next = step
		}
	}
	return next
}

type streamControls struct {
	freeze, disconnect, mute widget.Clickable
	volume                   widget.Float
	volumeEdited             bool
}

type deviceKey struct {
	ip, id string
	port   int
}

func receiverKey(device daemon.DeviceInfo) deviceKey {
	return deviceKey{ip: device.IP, id: device.DeviceID, port: device.Port}
}

type toast struct {
	message string
	isError bool
	expires time.Time
}

type commandResult struct {
	target, message string
	response        daemon.Response
}

func newDesktop(b desktopBackend, locale string, globalFreeze bool) *desktop {
	logo, err := png.Decode(bytes.NewReader(appIcon))
	if err != nil {
		panic(fmt.Errorf("decode DoubleTake icon: %w", err))
	}
	d := &desktop{
		backend: b, theme: desktopTheme(), german: locale == "de", globalFreeze: globalFreeze,
		icon: paint.NewImageOp(logo), glow: paint.NewImageOp(orbGlow()),
		sweep:   paint.NewImageOp(radarSweep()),
		devices: make(map[deviceKey]*widget.Clickable), sessions: make(map[string]*streamControls),
		deviceList: layout.List{Axis: layout.Vertical}, sessionList: layout.List{Axis: layout.Vertical},
		results: make(chan commandResult, 32), busy: make(map[string]bool),
	}
	d.search.SingleLine = true
	d.credential.SingleLine = true
	d.credential.Submit = true
	d.credential.Mask = '•'
	return d
}

func (d *desktop) tr(en, de string) string {
	if d.german {
		return de
	}
	return en
}

func (d *desktop) poll(now time.Time) {
	if now.Before(d.nextPoll) {
		return
	}
	previous := d.state
	d.state = d.backend.state()
	d.nextPoll = now.Add(statePollInterval)
	visibleDevices := make(map[deviceKey]bool, len(d.state.Devices))
	for _, receiver := range d.state.Devices {
		visibleDevices[receiverKey(receiver)] = true
	}
	for k := range d.devices {
		if !visibleDevices[k] {
			delete(d.devices, k)
		}
	}
	activeStreams := make(map[string]bool, len(d.state.Status.Streams))
	if d.state.Status.Error != d.lastError {
		d.notify(now, d.state.Status.Error, true)
		d.lastError = d.state.Status.Error
	}
	d.pending = nil
	for i := range d.state.Status.Streams {
		s := &d.state.Status.Streams[i]
		activeStreams[s.DeviceIP] = true
		if previous.FreezeArmedTarget == s.DeviceIP && d.state.FreezeArmedTarget == "" && s.Frozen == previous.FreezeArmedAction {
			if s.Frozen {
				d.notify(now, d.tr("Frame frozen.", "Bild eingefroren."), false)
			} else {
				d.notify(now, d.tr("Live video resumed.", "Livebild fortgesetzt."), false)
			}
		}
		if d.pending == nil && s.State == daemon.StatePINRequired {
			d.pending = s
		}
	}
	for target := range d.sessions {
		if !activeStreams[target] {
			delete(d.sessions, target)
		}
	}
}

func (d *desktop) notify(now time.Time, message string, isError bool) {
	if message != "" {
		d.toasts = append(d.toasts, toast{message: message, isError: isError, expires: now.Add(3900 * time.Millisecond)})
	}
}

func (d *desktop) response(gtx layout.Context, result daemon.Response, message string) {
	if result.OK {
		d.notify(gtx.Now, message, false)
	} else {
		d.notify(gtx.Now, result.Error, true)
	}
	d.nextPoll = time.Time{}
}

func (d *desktop) command(target, message string, run func() daemon.Response) {
	if d.busy[target] {
		return
	}
	d.busy[target] = true
	go func() {
		d.results <- commandResult{target: target, message: message, response: run()}
		// A static window draws no frame on its own until the next poll.
		if d.invalidate != nil {
			d.invalidate()
		}
	}()
}

func (d *desktop) update(gtx layout.Context) {
	for len(d.results) > 0 {
		result := <-d.results
		delete(d.busy, result.target)
		d.response(gtx, result.response, result.message)
	}
	for _, receiver := range d.state.Devices {
		if click := d.devices[receiverKey(receiver)]; d.pending == nil && !d.hasStream(receiver) && click != nil && click.Clicked(gtx) {
			name := receiverName(receiver.Name)
			d.response(gtx, d.backend.connect(receiver.IP, receiver.Port, receiver.DeviceID),
				fmt.Sprintf(d.tr("Connecting to %s…", "Verbindung zu %s wird hergestellt…"), name))
		}
	}
	oldPending := d.pending
	d.poll(gtx.Now)
	if d.pending != nil && (oldPending == nil || oldPending.DeviceIP != d.pending.DeviceIP) {
		d.credential.SetText("")
		d.focusCredential = true
	}
	if d.pending != nil {
		d.updateCredential(gtx)
		return
	}
	if d.clearSearch.Clicked(gtx) {
		d.search.SetText("")
		gtx.Execute(key.FocusCmd{Tag: &d.search})
	}
	if d.state.FreezeArmedTarget != "" {
		for {
			e, ok := gtx.Event(key.Filter{Name: key.NameEscape}, key.Filter{Name: key.NameReturn}, key.Filter{Name: key.NameEnter})
			if !ok {
				break
			}
			k := e.(key.Event)
			if k.State == key.Press {
				if k.Name == key.NameEscape {
					d.backend.disarmFreeze(d.state.FreezeArmedTarget)
					d.state.FreezeArmedTarget = ""
				} else if !d.globalFreeze {
					d.backend.focusedEnter()
				}
				d.nextPoll = time.Time{}
			}
		}
	}
	for _, s := range d.state.Status.Streams {
		d.updateStream(gtx, s)
	}
}

func (d *desktop) updateCredential(gtx layout.Context) {
	submit := d.submit.Clicked(gtx)
	for {
		e, ok := d.credential.Update(gtx)
		if !ok {
			break
		}
		if _, ok := e.(widget.SubmitEvent); ok {
			submit = true
		}
	}
	target := d.pending.DeviceIP
	if d.cancel.Clicked(gtx) {
		d.command(target, d.tr("Disconnected.", "Verbindung getrennt."), func() daemon.Response {
			return d.backend.disconnect(target)
		})
	} else if value := strings.TrimSpace(d.credential.Text()); submit && value != "" {
		d.response(gtx, d.backend.submitCredential(target, value), d.tr("Pairing details sent.", "Kopplungsdaten gesendet."))
	}
}

func (d *desktop) updateStream(gtx layout.Context, s daemon.StreamInfo) {
	controls := d.sessions[s.DeviceIP]
	if controls == nil {
		return
	}
	if controls.disconnect.Clicked(gtx) {
		d.command(s.DeviceIP, d.tr("Disconnected.", "Verbindung getrennt."), func() daemon.Response {
			return d.backend.disconnect(s.DeviceIP)
		})
	}
	if controls.freeze.Clicked(gtx) {
		if d.state.FreezeArmedTarget == s.DeviceIP {
			d.backend.disarmFreeze(s.DeviceIP)
			d.state.FreezeArmedTarget = ""
		} else if err := d.backend.armFreeze(s.DeviceIP, !s.Frozen); err != nil {
			d.notify(gtx.Now, err.Error(), true)
		} else {
			d.state.FreezeArmedTarget = s.DeviceIP
			d.state.FreezeArmedAction = !s.Frozen
		}
	}
	if controls.mute.Clicked(gtx) {
		message := d.tr("Audio muted.", "Ton stummgeschaltet.")
		if s.AudioMuted {
			message = d.tr("Audio unmuted.", "Ton eingeschaltet.")
		}
		d.command(s.DeviceIP, message, func() daemon.Response {
			return d.backend.setMuted(s.DeviceIP, !s.AudioMuted)
		})
	}
	if controls.volume.Update(gtx) {
		controls.volumeEdited = true
	}
	if controls.volumeEdited && !controls.volume.Dragging() {
		volume := int(math.Round(float64(controls.volume.Value * 100)))
		d.command(s.DeviceIP, "", func() daemon.Response {
			return d.backend.setVolume(s.DeviceIP, volume)
		})
		controls.volumeEdited = false
	}
}

func receiverName(name string) string {
	if name == "" {
		return "Apple TV"
	}
	return name
}
