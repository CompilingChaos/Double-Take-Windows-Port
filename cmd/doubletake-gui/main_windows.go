//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"doubletake/internal/airplay"

	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

func main() {
	runtime.LockOSThread()
	airplay.RestoreInterruptedSpeakerMute()

	appBackend, err := newBackend("127.0.0.1:0")
	if err != nil {
		showFatalWindow(err)
		return
	}

	if err := airplay.CheckFFmpeg(); err != nil {
		appBackend.setError(err)
	}

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
	stop := appBackend.start()
	defer stop()
	enterHook, hookErr := newGlobalEnterHook(appBackend.applyArmedFreeze)
	if hookErr != nil {
		appBackend.setError(hookErr)
	} else {
		appBackend.hook = enterHook
		defer enterHook.Close()
	}

	if err := appBackend.bind(w.Bind); err != nil {
		showFatalWindow(err)
		return
	}

	w.SetHtml(localizedHTML(appBackend.hook != nil))
	w.Run()
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
