//go:build windows || (linux && cgo && desktop)

package main

import (
	"context"
	_ "embed"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"doubletake/internal/airplay"
	"doubletake/internal/daemon"
)

type freezeHook interface {
	ArmedState() (string, bool)
	Arm(target string, frozen bool) error
	Disarm(target string)
}

//go:embed ui.html
var appHTML string

//go:embed icon-ui.png
var appIcon []byte

type backend struct {
	daemon *daemon.Daemon
	hook   freezeHook
	mu     sync.RWMutex
	err    string
}

type snapshot struct {
	Status            daemon.Response     `json:"status"`
	Devices           []daemon.DeviceInfo `json:"devices"`
	FreezeArmedTarget string              `json:"freeze_armed_target,omitempty"`
	FreezeArmedAction bool                `json:"freeze_armed_action"`
	SystemMessage     string              `json:"system_message,omitempty"`
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
	var target string
	var frozen bool
	if b.hook != nil {
		target, frozen = b.hook.ArmedState()
	}
	return snapshot{Status: status, Devices: devices.Devices, FreezeArmedTarget: target, FreezeArmedAction: frozen, SystemMessage: b.systemMessage()}
}

func (b *backend) connect(target string, port int) daemon.Response {
	return b.daemon.Execute(daemon.Request{Cmd: "connect", Target: target, Port: port, StartMuted: true})
}

func (b *backend) submitCredential(target, credential string) daemon.Response {
	return b.daemon.Execute(daemon.Request{Cmd: "connect", Target: target, Pin: credential})
}

func (b *backend) disconnect(target string) daemon.Response {
	b.disarmFreeze(target)
	return b.daemon.Execute(daemon.Request{Cmd: "disconnect", Target: target})
}

func (b *backend) setFrozen(target string, frozen bool) daemon.Response {
	b.disarmFreeze(target)
	cmd := "unfreeze"
	if frozen {
		cmd = "freeze"
	}
	return b.daemon.Execute(daemon.Request{Cmd: cmd, Target: target})
}

func (b *backend) armFreeze(target string, frozen bool) error {
	if b.hook == nil {
		if systemUILocale() == "de" {
			return fmt.Errorf("globale Eingabetaste ist nicht verfügbar: %s", b.systemMessage())
		}
		return fmt.Errorf("global Enter keyboard hook is unavailable: %s", b.systemMessage())
	}
	status := b.daemon.Execute(daemon.Request{Cmd: "status"})
	for _, stream := range status.Streams {
		if stream.DeviceIP == target && stream.State == daemon.StateStreaming && stream.Frozen != frozen {
			return b.hook.Arm(target, frozen)
		}
	}
	if systemUILocale() == "de" {
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

func (b *backend) disarmFreeze(target string) {
	if b.hook != nil {
		b.hook.Disarm(target)
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
			if systemUILocale() == "de" {
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

func (b *backend) bind(bind func(string, any) error) error {
	for _, binding := range []struct {
		name string
		fn   any
	}{
		{"getAppState", b.state},
		{"connectDevice", b.connect},
		{"submitCredential", b.submitCredential},
		{"disconnectDevice", b.disconnect},
		{"setStreamVolume", b.setVolume},
		{"setStreamMuted", b.setMuted},
		{"setStreamFrozen", b.setFrozen},
		{"armGlobalFreeze", b.armFreeze},
		{"disarmGlobalFreeze", b.disarmFreeze},
	} {
		if err := bind(binding.name, binding.fn); err != nil {
			return fmt.Errorf("bind %s: %w", binding.name, err)
		}
	}
	return nil
}

func localizedHTML(globalFreeze bool) string {
	return strings.NewReplacer(
		"__DOUBLETAKE_ICON__", "data:image/png;base64,"+base64.StdEncoding.EncodeToString(appIcon),
		"__DOUBLETAKE_LOCALE__", systemUILocale(),
		"__DOUBLETAKE_GLOBAL_FREEZE__", strconv.FormatBool(globalFreeze),
	).Replace(appHTML)
}
