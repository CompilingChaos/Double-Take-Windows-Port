//go:build windows

package airplay

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

	// A previous DoubleTake process may have died while it held this endpoint
	// muted. Restore that first, so the state recorded below is the user's own.
	if enumerator, err := newMMDeviceEnumerator(); err == nil {
		recoverInterruptedSpeakerMutes(enumerator.unmuteEndpoint)
		release(unsafe.Pointer(enumerator))
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
		// Record the mute before applying it: if this process is killed, the
		// next DoubleTake start can still return the speakers to the user.
		recordSpeakerMute(id)
		if err := volume.setMute(true); err != nil {
			clearSpeakerMute(id)
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
		return
	}
	clearRestoredSpeakerMute(id)
}

// RestoreInterruptedSpeakerMute unmutes speakers that a DoubleTake process
// muted for audio streaming and could not restore because it was killed,
// crashed, or was ended by a Windows shutdown. Endpoint mute persists across
// reboots, so without this the user's speakers would stay silent.
func RestoreInterruptedSpeakerMute() {
	done := make(chan struct{})
	go func() {
		defer close(done)
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		initialized, err := coInitialize()
		if err != nil {
			return
		}
		if initialized {
			defer procCoUninitialize.Call()
		}
		enumerator, err := newMMDeviceEnumerator()
		if err != nil {
			return
		}
		defer release(unsafe.Pointer(enumerator))
		speakerMutes.Lock()
		defer speakerMutes.Unlock()
		recoverInterruptedSpeakerMutes(enumerator.unmuteEndpoint)
	}()
	<-done
}

// recoverInterruptedSpeakerMutes must be called with speakerMutes held on a
// COM-initialized thread.
func recoverInterruptedSpeakerMutes(unmute func(string) error) {
	markers := readSpeakerMuteMarkers()
	if len(markers) == 0 {
		return
	}
	stale, keep := splitStaleSpeakerMuteMarkers(markers, os.Getpid(), doubleTakeProcessRunning)
	if len(stale) == 0 {
		return
	}
	active := make(map[string]bool, len(keep))
	for _, marker := range keep {
		active[marker.Device] = true
	}
	for _, marker := range stale {
		if active[marker.Device] {
			keep = append(keep, marker)
			continue
		}
		if err := unmute(marker.Device); err != nil {
			dbg("[AUDIO] restore speakers muted by interrupted session: %v", err)
			keep = append(keep, marker)
			continue
		}
		log.Printf("[AUDIO] restored speakers left muted by an interrupted DoubleTake session")
	}
	writeSpeakerMuteMarkers(keep)
}

func (e *iMMDeviceEnumerator) unmuteEndpoint(id string) error {
	device, err := e.getDevice(id)
	if err != nil {
		return err
	}
	defer release(unsafe.Pointer(device))
	volume, err := device.activateEndpointVolume()
	if err != nil {
		return err
	}
	defer release(unsafe.Pointer(volume))
	return volume.setMute(false)
}

func (e *iMMDeviceEnumerator) getDevice(id string) (*iMMDevice, error) {
	wideID, err := windows.UTF16PtrFromString(id)
	if err != nil {
		return nil, err
	}
	var ptr unsafe.Pointer
	hr, _, _ := syscall.SyscallN(e.lpVtbl.GetDevice,
		uintptr(unsafe.Pointer(e)),
		uintptr(unsafe.Pointer(wideID)),
		uintptr(unsafe.Pointer(&ptr)),
	)
	if failed(hr) {
		return nil, fmt.Errorf("IMMDeviceEnumerator.GetDevice: %s", hresultString(hr))
	}
	if ptr == nil {
		return nil, fmt.Errorf("IMMDeviceEnumerator.GetDevice returned no device")
	}
	return (*iMMDevice)(ptr), nil
}

// speakerMuteMarker records an endpoint this process muted.
type speakerMuteMarker struct {
	PID    int    `json:"pid"`
	Device string `json:"device"`
}

var speakerMuteMarkerPath = func() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "DoubleTake", "speaker-mute.json")
}

func readSpeakerMuteMarkers() []speakerMuteMarker {
	path := speakerMuteMarkerPath()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var markers []speakerMuteMarker
	if err := json.Unmarshal(data, &markers); err != nil {
		dbg("[AUDIO] ignoring unreadable speaker mute record %s: %v", path, err)
		return nil
	}
	return markers
}

func writeSpeakerMuteMarkers(markers []speakerMuteMarker) {
	path := speakerMuteMarkerPath()
	if path == "" {
		return
	}
	if len(markers) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			dbg("[AUDIO] remove speaker mute record: %v", err)
		}
		return
	}
	data, err := json.Marshal(markers)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		dbg("[AUDIO] create speaker mute record: %v", err)
		return
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		dbg("[AUDIO] write speaker mute record: %v", err)
		return
	}
	if err := os.Rename(temporary, path); err != nil {
		dbg("[AUDIO] write speaker mute record: %v", err)
	}
}

func recordSpeakerMute(device string) {
	markers := withoutSpeakerMuteMarker(readSpeakerMuteMarkers(), os.Getpid(), device)
	writeSpeakerMuteMarkers(append(markers, speakerMuteMarker{PID: os.Getpid(), Device: device}))
}

func clearSpeakerMute(device string) {
	writeSpeakerMuteMarkers(withoutSpeakerMuteMarker(readSpeakerMuteMarkers(), os.Getpid(), device))
}

// clearRestoredSpeakerMute runs after this process unmuted device. Records of
// ended processes for the same endpoint are settled by that unmute too; left
// behind, a later start would unmute speakers the user has since muted.
func clearRestoredSpeakerMute(device string) {
	writeSpeakerMuteMarkers(withoutRestoredSpeakerMutes(readSpeakerMuteMarkers(), os.Getpid(), device, doubleTakeProcessRunning))
}

// withoutRestoredSpeakerMutes drops device's records owned by self or by a
// process that is no longer running. Another live owner's record is kept.
func withoutRestoredSpeakerMutes(markers []speakerMuteMarker, self int, device string, running func(int) bool) []speakerMuteMarker {
	kept := markers[:0:0]
	for _, marker := range markers {
		if marker.Device == device && (marker.PID == self || !running(marker.PID)) {
			continue
		}
		kept = append(kept, marker)
	}
	return kept
}

func withoutSpeakerMuteMarker(markers []speakerMuteMarker, pid int, device string) []speakerMuteMarker {
	kept := markers[:0:0]
	for _, marker := range markers {
		if marker.PID != pid || marker.Device != device {
			kept = append(kept, marker)
		}
	}
	return kept
}

// splitStaleSpeakerMuteMarkers separates mutes whose owning process is gone
// from those still held by this or another running DoubleTake process. When
// several stale records name one endpoint, it is restored once.
func splitStaleSpeakerMuteMarkers(markers []speakerMuteMarker, self int, running func(int) bool) (stale, keep []speakerMuteMarker) {
	seen := map[string]bool{}
	for _, marker := range markers {
		if marker.Device == "" {
			continue
		}
		if marker.PID == self || running(marker.PID) {
			keep = append(keep, marker)
			continue
		}
		if !seen[marker.Device] {
			seen[marker.Device] = true
			stale = append(stale, marker)
		}
	}
	return stale, keep
}

// doubleTakeProcessRunning reports whether pid is a live DoubleTake process.
// Checking the executable name keeps a reused process ID from holding a stale
// mute forever. A process that cannot be inspected is assumed to be running.
func doubleTakeProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer windows.CloseHandle(handle)
	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return true
	}
	const stillActive = 259
	if exitCode != stillActive {
		return false
	}
	name := make([]uint16, windows.MAX_PATH)
	size := uint32(len(name))
	if err := windows.QueryFullProcessImageName(handle, 0, &name[0], &size); err != nil {
		return true
	}
	return strings.Contains(strings.ToLower(filepath.Base(windows.UTF16ToString(name[:size]))), "doubletake")
}
