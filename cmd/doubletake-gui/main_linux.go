//go:build linux && cgo && desktop

package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	webview "github.com/samcharles93/webview_go"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	w := webview.New(false)
	if w == nil {
		return fmt.Errorf("could not open the DoubleTake window; run it from an X11 or Wayland desktop session")
	}
	defer w.Destroy()
	if err := w.SetTitle("DoubleTake"); err != nil {
		return fmt.Errorf("set window title: %w", err)
	}
	if err := w.SetSize(1120, 760, webview.HintNone); err != nil {
		return fmt.Errorf("set window size: %w", err)
	}
	if err := w.SetSize(920, 640, webview.HintMin); err != nil {
		return fmt.Errorf("set minimum window size: %w", err)
	}

	dir, err := os.MkdirTemp("", "doubletake-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	app, err := newBackend(filepath.Join(dir, "control.sock"))
	if err != nil {
		return err
	}
	for _, tool := range []string{"gst-launch-1.0", "gst-inspect-1.0"} {
		if _, err := exec.LookPath(tool); err != nil {
			app.setError(fmt.Errorf("%s is required for screen mirroring; install gstreamer1.0-tools", tool))
			break
		}
	}
	stop := app.start()
	defer stop()
	enterHook, hookErr := newGlobalEnterHook(app.applyArmedFreeze)
	if hookErr != nil {
		log.Printf("global Enter unavailable: %v", hookErr)
	} else if enterHook != nil {
		app.hook = enterHook
		defer enterHook.Close()
	}
	if err := app.bind(w.Bind); err != nil {
		return err
	}
	if err := w.SetHtml(localizedHTML(app.hook != nil)); err != nil {
		return fmt.Errorf("load desktop interface: %w", err)
	}
	return w.Run()
}

func systemUILocale() string {
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if locale := strings.ToLower(os.Getenv(name)); locale != "" {
			if locale == "de" || strings.HasPrefix(locale, "de_") || strings.HasPrefix(locale, "de.") {
				return "de"
			}
			break
		}
	}
	return "en"
}
