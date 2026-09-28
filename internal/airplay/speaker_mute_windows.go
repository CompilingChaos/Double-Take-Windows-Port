//go:build windows

package airplay

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var iidIAudioEndpointVolume = windows.GUID{
	Data1: 0x5cdf2c82, Data2: 0x841e, Data3: 0x4546,
	Data4: [8]byte{0x97, 0x22, 0x0c, 0xf7, 0x40, 0x78, 0x22, 0x9a},
}

type iAudioEndpointVolume struct {
	lpVtbl *iAudioEndpointVolumeVtbl
}

type iAudioEndpointVolumeVtbl struct {
	iUnknownVtbl
	RegisterControlChangeNotify   uintptr
	UnregisterControlChangeNotify uintptr
	GetChannelCount               uintptr
	SetMasterVolumeLevel          uintptr
	SetMasterVolumeLevelScalar    uintptr
	GetMasterVolumeLevel          uintptr
	GetMasterVolumeLevelScalar    uintptr
	SetChannelVolumeLevel         uintptr
	SetChannelVolumeLevelScalar   uintptr
	GetChannelVolumeLevel         uintptr
	GetChannelVolumeLevelScalar   uintptr
	SetMute                       uintptr
	GetMute                       uintptr
}

func (d *iMMDevice) id() (string, error) {
	var ptr *uint16
	hr, _, _ := syscall.SyscallN(d.lpVtbl.GetId, uintptr(unsafe.Pointer(d)), uintptr(unsafe.Pointer(&ptr)))
	if failed(hr) {
		return "", fmt.Errorf("IMMDevice.GetId: %s", hresultString(hr))
	}
	if ptr == nil {
		return "", fmt.Errorf("IMMDevice.GetId returned no device ID")
	}
	defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(ptr)))
	return windows.UTF16PtrToString(ptr), nil
}

func (d *iMMDevice) activateEndpointVolume() (*iAudioEndpointVolume, error) {
	var ptr unsafe.Pointer
	hr, _, _ := syscall.SyscallN(d.lpVtbl.Activate,
		uintptr(unsafe.Pointer(d)),
		uintptr(unsafe.Pointer(&iidIAudioEndpointVolume)),
		clsctxAll,
		0,
		uintptr(unsafe.Pointer(&ptr)),
	)
	if failed(hr) {
		return nil, fmt.Errorf("Activate(IAudioEndpointVolume): %s", hresultString(hr))
	}
	if ptr == nil {
		return nil, fmt.Errorf("Activate(IAudioEndpointVolume) returned no interface")
	}
	return (*iAudioEndpointVolume)(ptr), nil
}

func (v *iAudioEndpointVolume) muted() (bool, error) {
	var state int32
	hr, _, _ := syscall.SyscallN(v.lpVtbl.GetMute,
		uintptr(unsafe.Pointer(v)), uintptr(unsafe.Pointer(&state)))
	if failed(hr) {
		return false, fmt.Errorf("IAudioEndpointVolume.GetMute: %s", hresultString(hr))
	}
	return state != 0, nil
}

func (v *iAudioEndpointVolume) setMute(muted bool) error {
	var state uintptr
	if muted {
		state = 1
	}
	hr, _, _ := syscall.SyscallN(v.lpVtbl.SetMute,
		uintptr(unsafe.Pointer(v)), state, 0)
	if failed(hr) {
		return fmt.Errorf("IAudioEndpointVolume.SetMute: %s", hresultString(hr))
	}
	return nil
}

type speakerMuteState struct {
	users    int
	wasMuted bool
}

var speakerMutes = struct {
	sync.Mutex
	devices map[string]*speakerMuteState
}{devices: make(map[string]*speakerMuteState)}

// muteLocalSpeakers mutes the render endpoint after loopback capture starts.
// Every concurrent audio stream holds a reference; the final release restores
// the endpoint's original mute state on the same COM-initialized capture thread.
func muteLocalSpeakers(device *iMMDevice) (func(), error) {
	id, err := device.id()
	if err != nil {
		return nil, err
	}

	speakerMutes.Lock()
	defer speakerMutes.Unlock()
	if state := speakerMutes.devices[id]; state != nil {
		state.users++
		return func() { restoreLocalSpeakers(device, id) }, nil
	}

	volume, err := device.activateEndpointVolume()
	if err != nil {
		return nil, err
	}
	defer release(unsafe.Pointer(volume))
	wasMuted, err := volume.muted()
	if err != nil {
		return nil, err
	}
	if !wasMuted {
		if err := volume.setMute(true); err != nil {
			return nil, err
		}
	}
	speakerMutes.devices[id] = &speakerMuteState{users: 1, wasMuted: wasMuted}
	return func() { restoreLocalSpeakers(device, id) }, nil
}

func restoreLocalSpeakers(device *iMMDevice, id string) {
	speakerMutes.Lock()
	defer speakerMutes.Unlock()
	state := speakerMutes.devices[id]
	if state == nil {
		return
	}
	state.users--
	if state.users > 0 {
		return
	}
	delete(speakerMutes.devices, id)
	if state.wasMuted {
		return
	}
	volume, err := device.activateEndpointVolume()
	if err != nil {
		dbg("[AUDIO] restore local speakers: %v", err)
		return
	}
	defer release(unsafe.Pointer(volume))
	if err := volume.setMute(false); err != nil {
		dbg("[AUDIO] restore local speakers: %v", err)
	}
}
