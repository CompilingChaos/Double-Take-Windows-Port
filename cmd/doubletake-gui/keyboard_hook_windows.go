//go:build windows

package main

import (
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	whKeyboardLL = 13
	wMKeyDown    = 0x0100
	wMSysKeyDown = 0x0104
	wMQuit       = 0x0012
	vKReturn     = 0x0d
)

var (
	user32              = windows.NewLazySystemDLL("user32.dll")
	setWindowsHookExW   = user32.NewProc("SetWindowsHookExW")
	callNextHookEx      = user32.NewProc("CallNextHookEx")
	unhookWindowsHookEx = user32.NewProc("UnhookWindowsHookEx")
	getMessageW         = user32.NewProc("GetMessageW")
	peekMessageW        = user32.NewProc("PeekMessageW")
	postThreadMessageW  = user32.NewProc("PostThreadMessageW")
)

type keyboardEvent struct {
	virtualKey uint32
	scanCode   uint32
	flags      uint32
	time       uint32
	extraInfo  uintptr
}

type windowPoint struct {
	x int32
	y int32
}

type windowMessage struct {
	window  uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	point   windowPoint
	private uint32
}

// globalEnterHook runs WH_KEYBOARD_LL on its own message-pumping OS thread.
// The callback does only a key check and state change, then returns control to
// the active application. It never reads or records other keystrokes.
type globalEnterHook struct {
	mu       sync.Mutex
	target   string
	frozen   bool
	closed   bool
	threadID uint32
	callback uintptr
	done     chan struct{}
	onEnter  func(string, bool)
}

func newGlobalEnterHook(onEnter func(string, bool)) (*globalEnterHook, error) {
	hook := &globalEnterHook{done: make(chan struct{}), onEnter: onEnter}
	hook.callback = windows.NewCallback(hook.handleKeyboard)
	ready := make(chan error, 1)
	go hook.run(ready)
	if err := <-ready; err != nil {
		return nil, err
	}
	return hook, nil
}

func (h *globalEnterHook) run(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(h.done)

	var message windowMessage
	// Creating the queue first makes PostThreadMessageW reliable during close.
	peekMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0, 0)
	threadID := windows.GetCurrentThreadId()
	installedHook, _, callErr := setWindowsHookExW.Call(whKeyboardLL, h.callback, 0, 0)
	if installedHook == 0 {
		ready <- fmt.Errorf("install global Enter keyboard hook: %w", callErr)
		return
	}
	defer unhookWindowsHookEx.Call(installedHook)

	h.mu.Lock()
	h.threadID = threadID
	h.mu.Unlock()
	ready <- nil
	for {
		result, _, _ := getMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if result == 0 || result == ^uintptr(0) {
			return
		}
	}
}

func (h *globalEnterHook) handleKeyboard(code int32, message, eventPtr uintptr) uintptr {
	if code >= 0 && (message == wMKeyDown || message == wMSysKeyDown) && eventPtr != 0 {
		event := (*keyboardEvent)(unsafe.Pointer(eventPtr))
		if event.virtualKey == vKReturn {
			h.mu.Lock()
			target := h.target
			frozen := h.frozen
			h.target = "" // key repeat and Enter-up cannot trigger a second action
			h.mu.Unlock()
			if target != "" {
				go h.onEnter(target, frozen)
			}
		}
	}
	// Never consume Enter: the program in focus receives it normally.
	result, _, _ := callNextHookEx.Call(0, uintptr(code), message, eventPtr)
	return result
}

func (h *globalEnterHook) Arm(target string, frozen bool) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return fmt.Errorf("global Enter hook is closed")
	}
	h.target = target
	h.frozen = frozen
	return nil
}

func (h *globalEnterHook) Disarm(target string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	if target == "" || h.target == target {
		h.target = ""
	}
	h.mu.Unlock()
}

func (h *globalEnterHook) ArmedState() (string, bool) {
	if h == nil {
		return "", false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.target, h.frozen
}

func (h *globalEnterHook) Close() {
	if h == nil {
		return
	}
	h.mu.Lock()
	if !h.closed {
		h.closed = true
		h.target = ""
		postThreadMessageW.Call(uintptr(h.threadID), wMQuit, 0, 0)
	}
	h.mu.Unlock()
	<-h.done
}
