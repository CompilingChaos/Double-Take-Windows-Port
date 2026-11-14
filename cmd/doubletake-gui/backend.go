//go:build windows || (linux && cgo && desktop)

package main

import (
	"context"
	_ "embed"
	"fmt"
	"sync"

	"doubletake/internal/airplay"
	"doubletake/internal/daemon"
)

type freezeHook interface {
	ArmedState() (string, bool)
	Arm(target string, frozen bool) error
	Disarm(target string)
}

//go:embed icon-ui.png
var appIcon []byte

type backend struct {
	daemon        *daemon.Daemon
	hook          freezeHook
	focusedFreeze freezeArm
	mu            sync.RWMutex
	err           string
}

type snapshot struct {
	Status            daemon.Response
	Devices           []daemon.DeviceInfo
	FreezeArmedTarget string
	FreezeArmedAction bool
	SystemMessage     string
}

func (b *backend) setError(err error) {
	if err == nil {
		return
	}
	b.mu.Lock()
	b.err = err.Error()
	b.mu.Unlock()
}

func (b *backend) systemMessage() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.err
}

func (b *backend) state() snapshot {
	status := b.daemon.Execute(daemon.Request{Cmd: "status"})
	devices := b.daemon.Execute(daemon.Request{Cmd: "devices"})
	target, frozen := b.freezeControl().ArmedState()
	return snapshot{Status: status, Devices: devices.Devices, FreezeArmedTarget: target, FreezeArmedAction: frozen, SystemMessage: b.systemMessage()}
}

func (b *backend) connect(target string, port int, deviceID string) daemon.Response {
	return b.daemon.Execute(daemon.Request{Cmd: "connect", Target: target, Port: port, DeviceID: deviceID, StartMuted: true})
}

func (b *backend) submitCredential(target, credential string) daemon.Response {
	return b.daemon.Execute(daemon.Request{Cmd: "connect", Target: target, Pin: credential})
}

func (b *backend) disconnect(target string) daemon.Response {
	b.disarmFreeze(target)
	return b.daemon.Execute(daemon.Request{Cmd: "disconnect", Target: target})
}

func (b *backend) setFrozen(target string, frozen bool) daemon.Response {
	cmd := "unfreeze"
	if frozen {
		cmd = "freeze"
	}
	return b.daemon.Execute(daemon.Request{Cmd: cmd, Target: target})
}

func (b *backend) armFreeze(target string, frozen bool) error {
	status := b.daemon.Execute(daemon.Request{Cmd: "status"})
	for _, stream := range status.Streams {
		if stream.DeviceIP == target && stream.State == daemon.StateStreaming && stream.Frozen != frozen {
			return b.freezeControl().Arm(target, frozen)
		}
	}
	if uiLocale() == "de" {
		return fmt.Errorf("Übertragung zu %s ist noch nicht bereit", target)
	}
	return fmt.Errorf("stream to %s is not ready to change freeze state", target)
}

func (b *backend) setVolume(target string, volume int) daemon.Response {
	return b.daemon.Execute(daemon.Request{Cmd: "volume", Target: target, Volume: &volume})
}

func (b *backend) setMuted(target string, muted bool) daemon.Response {
	cmd := "unmute"
	if muted {
		cmd = "mute"
	}
	return b.daemon.Execute(daemon.Request{Cmd: cmd, Target: target})
}

func (b *backend) freezeControl() freezeHook {
	if b.hook != nil {
		return b.hook
	}
	return &b.focusedFreeze
}

func (b *backend) disarmFreeze(target string) {
	b.freezeControl().Disarm(target)
}

func (b *backend) applyArmedFreeze(target string, frozen bool) {
	response := b.setFrozen(target, frozen)
	if response.OK {
		return
	}
	if uiLocale() == "de" {
		b.setError(fmt.Errorf("Einfrierzustand für %s konnte nicht geändert werden: %s", target, response.Error))
	} else {
		b.setError(fmt.Errorf("change freeze state for %s: %s", target, response.Error))
	}
}

func (b *backend) focusedEnter() {
	if target, frozen := b.focusedFreeze.take(); target != "" {
		b.applyArmedFreeze(target, frozen)
	}
}

func newBackend(socketPath string) (*backend, error) {
	d, err := daemon.New(daemon.Config{
		SocketPath:  socketPath,
		CredFile:    airplay.DefaultCredentialsPath(),
		CredBackend: "file",
		FPS:         30,
		HWAccel:     "auto",
		VideoCodec:  airplay.VideoCodecAuto,
		ShowCursor:  true,
	})
	if err != nil {
		return nil, err
	}
	return &backend{daemon: d}, nil
}

func (b *backend) start() func() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := b.daemon.Run(ctx); err != nil && ctx.Err() == nil {
			if uiLocale() == "de" {
				b.setError(fmt.Errorf("Hintergrunddienst wurde beendet: %w", err))
			} else {
				b.setError(fmt.Errorf("background service stopped: %w", err))
			}
		}
	}()
	return func() {
		cancel()
		b.daemon.Shutdown()
		<-done
	}
}
