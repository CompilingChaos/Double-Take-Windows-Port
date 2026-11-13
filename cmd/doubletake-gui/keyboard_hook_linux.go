//go:build linux && cgo && desktop

package main

/*
#cgo pkg-config: x11 xi
#include <X11/Xlib.h>
#include <X11/XKBlib.h>
#include <X11/extensions/XInput2.h>
#include <X11/keysym.h>

static int watch_enter(Display *display, int *opcode) {
	int event, error;
	if (!XQueryExtension(display, "XInputExtension", opcode, &event, &error))
		return 0;
	int major = 2, minor = 2;
	if (XIQueryVersion(display, &major, &minor) != Success ||
	    major < 2 || (major == 2 && minor < 2))
		return 0;

	unsigned char keys[XIMaskLen(XI_RawKeyPress)] = {0};
	XISetMask(keys, XI_RawKeyPress);
	XIEventMask mask = { XIAllMasterDevices, sizeof(keys), keys };
	for (int screen = 0; screen < ScreenCount(display); screen++) {
		if (XISelectEvents(display, RootWindow(display, screen), &mask, 1) != Success)
			return 0;
	}
	XFlush(display);
	return 1;
}

static int display_fd(Display *display) {
	return ConnectionNumber(display);
}

// Returns -1 when the queue is empty, 1 on a fresh Enter press, or 0 otherwise.
static int next_enter(Display *display, int opcode) {
	if (!XPending(display))
		return -1;
	XEvent event;
	XNextEvent(display, &event);
	XGenericEventCookie *cookie = &event.xcookie;
	if (event.type != GenericEvent || cookie->extension != opcode ||
	    cookie->evtype != XI_RawKeyPress || !XGetEventData(display, cookie))
		return 0;

	XIRawEvent *raw = cookie->data;
	KeySym key = XkbKeycodeToKeysym(display, raw->detail, 0, 0);
	int enter = !(raw->flags & XIKeyRepeat) && (key == XK_Return || key == XK_KP_Enter);
	XFreeEventData(display, cookie);
	return enter;
}
*/
import "C"

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

type globalEnterHook struct {
	freezeArm
	wake *os.File
	done chan struct{}
}

func newGlobalEnterHook(onEnter func(string, bool)) (*globalEnterHook, error) {
	if strings.EqualFold(os.Getenv("XDG_SESSION_TYPE"), "wayland") ||
		(os.Getenv("XDG_SESSION_TYPE") == "" && os.Getenv("WAYLAND_DISPLAY") != "") {
		return nil, nil
	}

	display := C.XOpenDisplay(nil)
	if display == nil {
		return nil, fmt.Errorf("open X11 display for the global Enter shortcut")
	}
	var opcode C.int
	if C.watch_enter(display, &opcode) == 0 {
		C.XCloseDisplay(display)
		return nil, fmt.Errorf("global Enter requires XInput 2.2")
	}

	readWake, writeWake, err := os.Pipe()
	if err != nil {
		C.XCloseDisplay(display)
		return nil, err
	}
	hook := &globalEnterHook{wake: writeWake, done: make(chan struct{})}
	go hook.run(display, opcode, readWake, onEnter)
	return hook, nil
}

func (h *globalEnterHook) run(display *C.Display, opcode C.int, wake *os.File, onEnter func(string, bool)) {
	defer close(h.done)
	defer wake.Close()
	defer C.XCloseDisplay(display)
	defer h.Disarm("")

	// The XInput subscription observes Enter without grabbing it from the
	// focused application. Only this goroutine uses the X11 connection.
	fds := []unix.PollFd{
		{Fd: int32(C.display_fd(display)), Events: unix.POLLIN},
		{Fd: int32(wake.Fd()), Events: unix.POLLIN},
	}
	for {
		for event := C.next_enter(display, opcode); event >= 0; event = C.next_enter(display, opcode) {
			if event == 1 {
				if target, frozen := h.take(); target != "" {
					onEnter(target, frozen)
				}
			}
		}

		_, err := unix.Poll(fds, -1)
		if err == unix.EINTR {
			continue
		}
		if err != nil || fds[1].Revents != 0 || fds[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
			return
		}
	}
}

func (h *globalEnterHook) Close() {
	_ = h.wake.Close()
	<-h.done
}
