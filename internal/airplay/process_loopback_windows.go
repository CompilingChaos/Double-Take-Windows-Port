//go:build windows

package airplay

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Process loopback is mixed by Windows independently of the selected render
// endpoint. This lets us silence the speakers without silencing the capture on
// drivers whose endpoint loopback tap is after the endpoint mute control.
var (
	processLoopbackDLL          = windows.NewLazySystemDLL("Mmdevapi.dll")
	activateAudioInterfaceAsync = processLoopbackDLL.NewProc("ActivateAudioInterfaceAsync")
	completionHandlerIID        = windows.GUID{Data1: 0x41d949ab, Data2: 0x9862, Data3: 0x444a, Data4: [8]byte{0x80, 0xf6, 0xc2, 0x61, 0x33, 0x4d, 0xa5, 0xeb}}
	unknownIID                  = windows.GUID{Data1: 0x00000000, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xc0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	agileObjectIID              = windows.GUID{Data1: 0x94ea2b94, Data2: 0xe9cc, Data3: 0x49e0, Data4: [8]byte{0xc0, 0xff, 0xee, 0x64, 0xca, 0x8f, 0x5b, 0x90}}
	processCompletionVtbl       = processCompletionHandlerVtbl{
		iUnknownVtbl: iUnknownVtbl{
			QueryInterface: windows.NewCallback(processCompletionQueryInterface),
			AddRef:         windows.NewCallback(processCompletionAddRef),
			Release:        windows.NewCallback(processCompletionRelease),
		},
		ActivateCompleted: windows.NewCallback(processCompletionActivateCompleted),
	}
)

type processCompletionHandlerVtbl struct {
	iUnknownVtbl
	ActivateCompleted uintptr
}

type processCompletionHandler struct {
	lpVtbl    *processCompletionHandlerVtbl
	refs      atomic.Int32
	completed chan processActivationResult
}

type processActivationResult struct {
	client *iAudioClient
	err    error
}

type processLoopbackActivationParams struct {
	activationType uint32
	processID      uint32
	mode           uint32
}

// PROPVARIANT with vt=VT_BLOB; the BLOB is aligned to eight bytes on amd64.
type processLoopbackVariant struct {
	vt       uint16
	reserved [6]byte
	size     uint32
	padding  [4]byte
	data     uintptr
}

type processActivationOperation struct {
	lpVtbl *processActivationOperationVtbl
}

type processActivationOperationVtbl struct {
	iUnknownVtbl
	GetActivateResult uintptr
}

type processActivationRequest struct {
	path    *uint16
	params  *processLoopbackActivationParams
	variant *processLoopbackVariant
	handler *processCompletionHandler
	pin     runtime.Pinner
}

var retainedProcessCallbacks = struct {
	sync.Mutex
	requests []*processActivationRequest
}{}

func processCompletionQueryInterface(this, iid, out uintptr) uintptr {
	if out == 0 || iid == 0 {
		return 0x80004003
	}
	*(*uintptr)(unsafe.Pointer(out)) = 0
	want := *(*windows.GUID)(unsafe.Pointer(iid))
	if want != unknownIID && want != completionHandlerIID && want != agileObjectIID {
		return 0x80004002
	}
	*(*uintptr)(unsafe.Pointer(out)) = this
	processCompletionAddRef(this)
	return 0
}

func processCompletionAddRef(this uintptr) uintptr {
	h := (*processCompletionHandler)(unsafe.Pointer(this))
	return uintptr(h.refs.Add(1))
}

func processCompletionRelease(this uintptr) uintptr {
	h := (*processCompletionHandler)(unsafe.Pointer(this))
	return uintptr(h.refs.Add(-1))
}

func processCompletionActivateCompleted(this, operation uintptr) uintptr {
	h := (*processCompletionHandler)(unsafe.Pointer(this))
	op := (*processActivationOperation)(unsafe.Pointer(operation))
	var activationResult uint32
	var client unsafe.Pointer
	hr, _, _ := syscall.SyscallN(op.lpVtbl.GetActivateResult,
		operation, uintptr(unsafe.Pointer(&activationResult)), uintptr(unsafe.Pointer(&client)))
	result := processActivationResult{}
	if failed(hr) {
		result.err = fmt.Errorf("process loopback activation result: %s", hresultString(hr))
	} else if failed(uintptr(activationResult)) {
		result.err = fmt.Errorf("process loopback activation: %s", hresultString(uintptr(activationResult)))
	} else if client == nil {
		result.err = fmt.Errorf("process loopback returned no audio client")
	} else {
		result.client = (*iAudioClient)(client)
	}
	h.completed <- result
	return 0
}

func activateProcessLoopbackClient() (*iAudioClient, error) {
	path, err := windows.UTF16PtrFromString(`VAD\Process_Loopback`)
	if err != nil {
		return nil, err
	}
	request := &processActivationRequest{
		path:    path,
		params:  &processLoopbackActivationParams{activationType: 1, processID: uint32(os.Getpid()), mode: 1},
		handler: &processCompletionHandler{lpVtbl: &processCompletionVtbl, completed: make(chan processActivationResult, 1)},
	}
	request.variant = &processLoopbackVariant{vt: 65, size: uint32(unsafe.Sizeof(*request.params)), data: uintptr(unsafe.Pointer(request.params))}
	request.pin.Pin(request.path)
	request.pin.Pin(request.params)
	request.pin.Pin(request.variant)
	request.pin.Pin(request.handler)
	handler := request.handler
	handler.refs.Store(1)
	var operation unsafe.Pointer
	hr, _, _ := activateAudioInterfaceAsync.Call(
		uintptr(unsafe.Pointer(request.path)), uintptr(unsafe.Pointer(&iidIAudioClient)),
		uintptr(unsafe.Pointer(request.variant)), uintptr(unsafe.Pointer(handler)),
		uintptr(unsafe.Pointer(&operation)))
	if failed(hr) {
		request.pin.Unpin()
		if operation != nil {
			release(operation)
		}
		return nil, fmt.Errorf("ActivateAudioInterfaceAsync(process loopback): %s", hresultString(hr))
	}
	timedOut := false
	defer func() {
		if !timedOut {
			request.pin.Unpin()
		}
	}()
	if operation != nil {
		defer release(operation)
	}
	// Keep every activation argument live until the asynchronous callback returns.
	defer runtime.KeepAlive(request)
	select {
	case result := <-handler.completed:
		return result.client, result.err
	case <-time.After(10 * time.Second):
		timedOut = true
		// A late callback still needs its handler. Retain it until process exit.
		retainedProcessCallbacks.Lock()
		retainedProcessCallbacks.requests = append(retainedProcessCallbacks.requests, request)
		retainedProcessCallbacks.Unlock()
		return nil, fmt.Errorf("process loopback activation timed out")
	}
}
