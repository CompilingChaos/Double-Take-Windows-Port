//go:build windows

package main

import (
	"os"
	"unsafe"

	"doubletake/internal/airplay"
	"gioui.org/app"
	"golang.org/x/sys/windows"
)

func main() {
	go func() {
		if err := run(); err != nil {
			showFatalWindow(err)
			os.Exit(1)
		}
		os.Exit(0)
	}()
	app.Main()
}

func run() error {
	airplay.RestoreInterruptedSpeakerMute()
	b, err := newBackend("127.0.0.1:0")
	if err != nil {
		return err
	}
	b.setError(airplay.CheckFFmpeg())
	stop := b.start()
	defer stop()
	hook, err := newGlobalEnterHook(b.applyArmedFreeze)
	if err != nil {
		b.setError(err)
	} else {
		b.hook = hook
		defer hook.Close()
	}
	return runWindow(b)
}

func configureWindow(w *app.Window) {
	w.Option(app.Maximized.Option())
}

func configureView(view app.ViewEvent) {
	if v, ok := view.(app.Win32ViewEvent); ok && v.Valid() {
		setDarkTitleBar(v.HWND)
		const wmSetIcon = 0x0080
		user := windows.NewLazySystemDLL("user32.dll")
		var module windows.Handle
		if err := windows.GetModuleHandleEx(0, nil, &module); err != nil {
			return
		}
		icon, _, _ := user.NewProc("LoadIconW").Call(uintptr(module), 1)
		if icon != 0 {
			send := user.NewProc("PostMessageW")
			send.Call(v.HWND, wmSetIcon, 0, icon)
			send.Call(v.HWND, wmSetIcon, 1, icon)
		}
	}
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

func setDarkTitleBar(window uintptr) {
	const dwmwaUseImmersiveDarkMode = 20
	enabled := int32(1)
	proc := windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")
	_, _, _ = proc.Call(
		window,
		dwmwaUseImmersiveDarkMode,
		uintptr(unsafe.Pointer(&enabled)),
		unsafe.Sizeof(enabled),
	)
}

func showFatalWindow(err error) {
	message := "DoubleTake could not start:\n\n"
	if uiLocale() == "de" {
		message = "DoubleTake konnte nicht gestartet werden:\n\n"
	}
	text, _ := windows.UTF16PtrFromString(message + err.Error())
	title, _ := windows.UTF16PtrFromString("DoubleTake")
	_, _ = windows.MessageBox(0, text, title, windows.MB_OK|windows.MB_ICONERROR)
}
