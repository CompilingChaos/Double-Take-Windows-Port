//go:build linux && cgo && desktop

package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gioui.org/app"
)

func main() {
	go func() {
		if err := run(); err != nil {
			log.Fatal(err)
		}
		os.Exit(0)
	}()
	app.Main()
}

func run() error {
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
	return runWindow(app)
}

func configureWindow(*app.Window) {}
func configureView(app.ViewEvent) {}

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
