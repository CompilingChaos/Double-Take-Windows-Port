//go:build windows

package main

import (
	"context"
	_ "embed"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"doubletake/internal/airplay"
	"doubletake/internal/daemon"

	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

//go:embed ui.html
var appHTML string

//go:embed icon-ui.png
var appIcon []byte

type backend struct {
	daemon *daemon.Daemon
	hook   *globalEnterHook
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
	target, frozen := b.hook.ArmedState()
	return snapshot{Status: status, Devices: devices.Devices, FreezeArmedTarget: target, FreezeArmedAction: frozen, SystemMessage: b.systemMessage()}
}

func (b *backend) connect(target string, port int) daemon.Response {
	return b.daemon.Execute(daemon.Request{Cmd: "connect", Target: target, Port: port, StartMuted: true})
}

func (b *backend) submitCredential(target, credential string) daemon.Response {
	return b.daemon.Execute(daemon.Request{Cmd: "connect", Target: target, Pin: credential})
}

func (b *backend) disconnect(target string) daemon.Response {
	b.hook.Disarm(target)
	return b.daemon.Execute(daemon.Request{Cmd: "disconnect", Target: target})
}

func (b *backend) setFrozen(target string, frozen bool) daemon.Response {
	b.hook.Disarm(target)
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
	b.hook.Disarm(target)
}

func main() {
	runtime.LockOSThread()

	appBackend, err := newBackend()
	if err != nil {
		showFatalWindow(err)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		if runErr := appBackend.daemon.Run(ctx); runErr != nil && ctx.Err() == nil {
			if systemUILocale() == "de" {
				appBackend.setError(fmt.Errorf("Hintergrunddienst wurde beendet: %w", runErr))
			} else {
				appBackend.setError(fmt.Errorf("background service stopped: %w", runErr))
			}
		}
	}()

	cacheDir, _ := os.UserCacheDir()
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		DataPath:  filepath.Join(cacheDir, "DoubleTake", "WebView2"),
		WindowOptions: webview2.WindowOptions{
			Title:  "DoubleTake",
			Width:  1120,
			Height: 760,
			IconId: 1,
			Center: true,
		},
	})
	if w == nil {
		appBackend.daemon.Shutdown()
		if systemUILocale() == "de" {
			showFatalWindow(fmt.Errorf("Microsoft Edge WebView2 konnte nicht gestartet werden"))
			return
		}
		showFatalWindow(fmt.Errorf("Microsoft Edge WebView2 could not be started"))
		return
	}
	defer w.Destroy()
	setDarkTitleBar(w.Window())
	w.SetSize(920, 640, webview2.HintMin)
	maximizeWindow(w.Window())
	enterHook, hookErr := newGlobalEnterHook(func(target string, frozen bool) {
		response := appBackend.setFrozen(target, frozen)
		if !response.OK {
			if systemUILocale() == "de" {
				appBackend.setError(fmt.Errorf("Einfrierzustand für %s konnte nicht geändert werden: %s", target, response.Error))
			} else {
				appBackend.setError(fmt.Errorf("change freeze state for %s: %s", target, response.Error))
			}
		}
	})
	if hookErr != nil {
		appBackend.setError(hookErr)
	}
	appBackend.hook = enterHook

	mustBind(w, "getAppState", appBackend.state)
	mustBind(w, "connectDevice", appBackend.connect)
	mustBind(w, "submitCredential", appBackend.submitCredential)
	mustBind(w, "disconnectDevice", appBackend.disconnect)
	mustBind(w, "setStreamVolume", appBackend.setVolume)
	mustBind(w, "setStreamMuted", appBackend.setMuted)
	mustBind(w, "armGlobalFreeze", appBackend.armFreeze)
	mustBind(w, "disarmGlobalFreeze", appBackend.disarmFreeze)
	iconDataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(appIcon)
	localizedHTML := strings.NewReplacer(
		"__DOUBLETAKE_ICON__", iconDataURL,
		"__DOUBLETAKE_LOCALE__", systemUILocale(),
	).Replace(appHTML)
	w.SetHtml(localizedHTML)
	w.Run()

	enterHook.Close()
	cancel()
	appBackend.daemon.Shutdown()
}

func systemUILocale() string {
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetUserDefaultUILanguage")
	langID, _, _ := proc.Call()
	const primaryLanguageMask = 0x3ff
	const germanLanguage = 0x07
	if langID&primaryLanguageMask == germanLanguage {
		return "de"
	}
	return "en"
}

func newBackend() (*backend, error) {
	d, err := daemon.New(daemon.Config{
		SocketPath:  "127.0.0.1:0",
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
	b := &backend{daemon: d}
	if err := airplay.CheckFFmpeg(); err != nil {
		b.setError(err)
	}
	return b, nil
}

func mustBind(w webview2.WebView, name string, fn any) {
	if err := w.Bind(name, fn); err != nil {
		log.Fatalf("bind %s: %v", name, err)
	}
}

func setDarkTitleBar(window unsafe.Pointer) {
	const dwmwaUseImmersiveDarkMode = 20
	enabled := int32(1)
	proc := windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")
	_, _, _ = proc.Call(
		uintptr(window),
		dwmwaUseImmersiveDarkMode,
		uintptr(unsafe.Pointer(&enabled)),
		unsafe.Sizeof(enabled),
	)
}

func maximizeWindow(window unsafe.Pointer) {
	const swMaximize = 3
	proc := windows.NewLazySystemDLL("user32.dll").NewProc("ShowWindow")
	_, _, _ = proc.Call(uintptr(window), swMaximize)
}

func showFatalWindow(err error) {
	message := "DoubleTake could not start:\n\n"
	if systemUILocale() == "de" {
		message = "DoubleTake konnte nicht gestartet werden:\n\n"
	}
	text, _ := windows.UTF16PtrFromString(message + err.Error())
	title, _ := windows.UTF16PtrFromString("DoubleTake")
	_, _ = windows.MessageBox(0, text, title, windows.MB_OK|windows.MB_ICONERROR)
}
