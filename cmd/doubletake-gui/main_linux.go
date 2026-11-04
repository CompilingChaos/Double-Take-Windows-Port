//go:build linux && cgo && desktop

package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	webview "github.com/tester305/webview_go"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	w := webview.New(false)
	if w.Window() == nil {
		return fmt.Errorf("could not open the DoubleTake window; run it from an X11 or Wayland desktop session")
	}
	defer w.Destroy()
	w.SetTitle("DoubleTake")
	w.SetSize(1120, 760, webview.HintNone)
	w.SetSize(920, 640, webview.HintMin)

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
	if err := app.bind(w.Bind); err != nil {
		return err
	}
	stop := app.start()
	defer stop()
	w.SetHTML(localizedHTML(false))
	w.Run()
	return nil
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
