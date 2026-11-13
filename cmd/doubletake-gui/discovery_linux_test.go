//go:build linux && cgo && desktop

package main

import (
	"os"
	"runtime"
	"testing"
	"time"

	"doubletake/internal/daemon"

	webview "github.com/samcharles93/webview_go"
)

func TestDiscoveredReceiverAppearsInWindow(t *testing.T) {
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		t.Skip("requires a graphical session")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	w := webview.New(false)
	if w == nil {
		t.Fatal("could not start WebKitGTK")
	}
	defer w.Destroy()

	if err := w.Bind("getAppState", func() snapshot {
		return snapshot{Devices: []daemon.DeviceInfo{{
			Name: "Test Apple TV", Model: "AppleTV6,2", IP: "192.0.2.1", Port: 7000,
		}}}
	}); err != nil {
		t.Fatal(err)
	}

	rendered := false
	if err := w.Bind("receiverRendered", func() {
		rendered = true
		_ = w.Terminate()
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.Init(`
		document.addEventListener('DOMContentLoaded', () => {
			const list = document.getElementById('deviceList');
			const check = () => {
				if (list.querySelector('.device-name')?.textContent === 'Test Apple TV') {
					observer.disconnect();
					receiverRendered();
				}
			};
			const observer = new MutationObserver(check);
			observer.observe(list, {childList: true, subtree: true});
			check();
		});
	`); err != nil {
		t.Fatal(err)
	}
	if err := w.SetHtml(localizedHTML(false)); err != nil {
		t.Fatal(err)
	}

	timeoutFinished := make(chan struct{})
	timer := time.AfterFunc(12*time.Second, func() {
		defer close(timeoutFinished)
		_ = w.Terminate()
	})
	err := w.Run()
	if !timer.Stop() {
		<-timeoutFinished
	}
	if err != nil {
		t.Fatal(err)
	}
	if !rendered {
		t.Fatal("the receiver returned by the discovery service never appeared in the list")
	}
}
