//go:build windows

package airplay

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"math"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	coinitMultithreaded = 0x0
	clsctxAll           = 0x17
	rpcEChangedMode     = 0x80010106

	eRender  = 0
	eConsole = 0

	audclntSharemodeShared           = 0
	audclntStreamflagsLoopback       = 0x00020000
	audclntStreamflagsAutoconvertPCM = 0x80000000
	audclntBufferflagsSilent         = 0x00000002
	audclntBufferflagsTimestampError = 0x00000004

	waveFormatPCM           = 0x0001
	waveFormatIEEEFloat     = 0x0003
	waveFormatExtensibleTag = 0xfffe

	referenceTime100ms = 1000000
)

var (
	ole32                  = windows.NewLazySystemDLL("ole32.dll")
	procCoInitializeEx     = ole32.NewProc("CoInitializeEx")
	procCoUninitialize     = ole32.NewProc("CoUninitialize")
	procCoCreateInstance   = ole32.NewProc("CoCreateInstance")
	procCoTaskMemFree      = ole32.NewProc("CoTaskMemFree")
	clsidMMDeviceEnum      = windows.GUID{Data1: 0xbcde0395, Data2: 0xe52f, Data3: 0x467c, Data4: [8]byte{0x8e, 0x3d, 0xc4, 0x57, 0x92, 0x91, 0x69, 0x2e}}
	iidIMMDeviceEnumerator = windows.GUID{Data1: 0xa95664d2, Data2: 0x9614, Data3: 0x4f35, Data4: [8]byte{0xa7, 0x46, 0xde, 0x8d, 0xb6, 0x36, 0x17, 0xe6}}
	iidIAudioClient        = windows.GUID{Data1: 0x1cb9ad4c, Data2: 0xdbfa, Data3: 0x4c32, Data4: [8]byte{0xb1, 0x78, 0xc2, 0xf5, 0x68, 0xa7, 0x03, 0xb2}}
	iidIAudioCaptureClient = windows.GUID{Data1: 0xc8adbd64, Data2: 0xe71e, Data3: 0x48a0, Data4: [8]byte{0xa4, 0xde, 0x18, 0x5c, 0x39, 0x5c, 0xd3, 0x17}}
)

type iUnknownVtbl struct {
	QueryInterface uintptr
	AddRef         uintptr
	Release        uintptr
}

type iMMDeviceEnumerator struct {
	lpVtbl *iMMDeviceEnumeratorVtbl
}

type iMMDeviceEnumeratorVtbl struct {
	iUnknownVtbl
	EnumAudioEndpoints       uintptr
	GetDefaultAudioEndpoint  uintptr
	GetDevice                uintptr
	RegisterEndpointCallback uintptr
	UnregisterEndpointNotify uintptr
}

type iMMDevice struct {
	lpVtbl *iMMDeviceVtbl
}

type iMMDeviceVtbl struct {
	iUnknownVtbl
	Activate          uintptr
	OpenPropertyStore uintptr
	GetId             uintptr
	GetState          uintptr
}

type iAudioClient struct {
	lpVtbl *iAudioClientVtbl
}

type iAudioClientVtbl struct {
	iUnknownVtbl
	Initialize        uintptr
	GetBufferSize     uintptr
	GetStreamLatency  uintptr
	GetCurrentPadding uintptr
	IsFormatSupported uintptr
	GetMixFormat      uintptr
	GetDevicePeriod   uintptr
	Start             uintptr
	Stop              uintptr
	Reset             uintptr
	SetEventHandle    uintptr
	GetService        uintptr
}

type iAudioCaptureClient struct {
	lpVtbl *iAudioCaptureClientVtbl
}

type iAudioCaptureClientVtbl struct {
	iUnknownVtbl
	GetBuffer         uintptr
	ReleaseBuffer     uintptr
	GetNextPacketSize uintptr
}

type waveFormatEx struct {
	FormatTag      uint16
	Channels       uint16
	SamplesPerSec  uint32
	AvgBytesPerSec uint32
	BlockAlign     uint16
	BitsPerSample  uint16
	CbSize         uint16
}

type waveFormatExtensible struct {
	// WAVEFORMATEX occupies 18 bytes in the Windows ABI. Embedding the Go
	// waveFormatEx struct would add trailing padding and shift SubFormat.
	FormatTag      uint16
	Channels       uint16
	SamplesPerSec  uint32
	AvgBytesPerSec uint32
	BlockAlign     uint16
	BitsPerSample  uint16
	CbSize         uint16
	Samples        uint16
	ChannelMask    uint32
	SubFormat      windows.GUID
}

// The native WAVEFORMATEXTENSIBLE SubFormat GUID begins at byte 24.
var _ [24 - unsafe.Offsetof(waveFormatExtensible{}.SubFormat)]byte
var _ [unsafe.Offsetof(waveFormatExtensible{}.SubFormat) - 24]byte

type stereoSample struct {
	left  float64
	right float64
}

type audioResampler struct {
	inRate float64
	phase  float64
	buf    []stereoSample
}

func StartAudioCapture(ctx context.Context, testTone bool, codec AudioCodec) (*AudioCapture, error) {
	if codec != AudioCodecALAC {
		return nil, fmt.Errorf("%w: Windows build currently supports ALAC capture only", ErrAACELDUnavailable)
	}
	if testTone {
		return startWindowsTestToneCapture(ctx, codec)
	}
	return startWindowsAudioCapture(ctx, codec)
}

func startWindowsAudioCapture(parent context.Context, codec AudioCodec) (*AudioCapture, error) {
	ctx, cancel := context.WithCancel(parent)
	// The capture thread must never wait for the network sender: process
	// loopback buffers only one ~10 ms engine period. A timestamped queue keeps
	// the two apart and lets the sender schedule every frame by its capture time,
	// exactly like the GStreamer RTP/ONVIF path on Linux.
	queue := newTimestampedPCMQueue(audioCaptureStallBuffer)
	waitCh := make(chan struct{})
	ready := make(chan error, 1)
	var stopped atomic.Bool

	ac := &AudioCapture{
		pcmFrames: queue,
		cancel:    cancel,
		stopFn: func() {
			if stopped.CompareAndSwap(false, true) {
				cancel()
				queue.close(nil)
			}
		},
		waitCh: waitCh,
		codec:  codec,
	}

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(waitCh)
		ac.waitErr = runWASAPILoopback(ctx, queue, ready)
		queue.close(ac.waitErr)
	}()

	if err := <-ready; err != nil {
		cancel()
		return nil, err
	}
	return ac, nil
}

func startWindowsTestToneCapture(parent context.Context, codec AudioCodec) (*AudioCapture, error) {
	ctx, cancel := context.WithCancel(parent)
	pr, pw := io.Pipe()
	waitCh := make(chan struct{})
	var stopped atomic.Bool

	ac := &AudioCapture{
		pcmPipe: pr,
		cancel:  cancel,
		stopFn: func() {
			if stopped.CompareAndSwap(false, true) {
				cancel()
				_ = pr.Close()
				_ = pw.Close()
			}
		},
		waitCh: waitCh,
		codec:  codec,
	}

	go func() {
		defer close(waitCh)
		ac.waitErr = runWindowsTestTone(ctx, pw)
		if ac.waitErr != nil {
			_ = pw.CloseWithError(ac.waitErr)
		} else {
			_ = pw.Close()
		}
	}()

	dbg("[AUDIO] using native Windows test tone (440 Hz sine wave)")
	return ac, nil
}

func runWindowsTestTone(ctx context.Context, out *io.PipeWriter) error {
	const (
		sampleRate = 44100
		frequency  = 440.0
		chunk      = 352
	)
	ticker := time.NewTicker(time.Duration(chunk) * time.Second / sampleRate)
	defer ticker.Stop()

	var phase float64
	step := 2 * math.Pi * frequency / sampleRate
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}

		pcm := make([]byte, 0, chunk*4)
		for i := 0; i < chunk; i++ {
			v := math.Sin(phase) * 0.25
			appendS16Stereo(&pcm, v, v)
			phase += step
			if phase >= 2*math.Pi {
				phase -= 2 * math.Pi
			}
		}
		if _, err := out.Write(pcm); err != nil {
			return err
		}
	}
}

func runWASAPILoopback(ctx context.Context, queue *timestampedPCMQueue, ready chan<- error) error {
	comInitialized, err := coInitialize()
	if err != nil {
		ready <- err
		return err
	}
	if comInitialized {
		defer procCoUninitialize.Call()
	}

	enumerator, err := newMMDeviceEnumerator()
	if err != nil {
		ready <- err
		return err
	}
	defer release(unsafe.Pointer(enumerator))

	device, err := enumerator.getDefaultAudioEndpoint()
	if err != nil {
		ready <- err
		return err
	}
	defer release(unsafe.Pointer(device))

	opened, err := openWindowsLoopbackCapture(device)
	if err != nil {
		ready <- err
		return err
	}
	client, captureClient, formatPtr := opened.client, opened.capture, opened.format
	defer release(unsafe.Pointer(client))
	defer release(unsafe.Pointer(captureClient))
	if opened.freeFormat {
		defer procCoTaskMemFree.Call(uintptr(formatPtr))
	}
	format := (*waveFormatEx)(formatPtr)
	dbg("[AUDIO] WASAPI capture format: %s", describeWaveFormat(formatPtr))

	restorePriority := raiseCaptureThreadPriority()
	defer restorePriority()

	if err := client.start(); err != nil {
		ready <- err
		return err
	}
	restoreSpeakers := func() {}
	if opened.processLoopback {
		restoreSpeakers, err = muteLocalSpeakers(device)
		if err != nil {
			client.stop()
			muteErr := fmt.Errorf("mute local speakers: %w", err)
			ready <- muteErr
			return muteErr
		}
	} else {
		// Endpoint loopback taps the stream after the endpoint mute on many
		// drivers, so muting the speakers here would send pure silence.
		log.Printf("[AUDIO] Windows process loopback is unavailable; local speakers stay on while streaming audio")
	}
	defer func() {
		client.stop()
		restoreSpeakers()
	}()

	logged := false
	ready <- nil

	timeline := newWindowsCaptureTimeline(format.SamplesPerSec, queue)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		packetFrames, err := captureClient.nextPacketSize()
		if err != nil {
			return err
		}
		if packetFrames == 0 {
			timeline.fillIdle(time.Now())
			time.Sleep(2 * time.Millisecond)
			continue
		}

		packet, err := captureClient.getBuffer()
		if err != nil {
			return err
		}
		now := time.Now()
		pts, trusted := capturePacketPTS(now, performanceCounter100ns(), packet.qpcPosition,
			packet.flags&audclntBufferflagsTimestampError != 0)
		if !trusted {
			pts = now
		}
		if !logged {
			dbg("[AUDIO] WASAPI loopback started (%d frames first packet, device timestamps=%t)", packet.frames, trusted)
			logged = true
		}

		var samples []stereoSample
		if packet.flags&audclntBufferflagsSilent != 0 {
			samples = make([]stereoSample, int(packet.frames))
		} else {
			samples = decodeWASAPIFrames(packet.data, int(packet.frames), formatPtr)
		}
		if err := captureClient.releaseBuffer(packet.frames); err != nil {
			return err
		}
		timeline.push(samples, pts, trusted)
	}
}

type windowsLoopbackCapture struct {
	client          *iAudioClient
	capture         *iAudioCaptureClient
	format          unsafe.Pointer
	freeFormat      bool
	processLoopback bool
}

func openWindowsLoopbackCapture(device *iMMDevice) (windowsLoopbackCapture, error) {
	// Process loopback is independent of the physical endpoint's mute tap and
	// captures all render streams except those from DoubleTake itself.
	var err error
	for _, rate := range processLoopbackRates(device) {
		var client *iAudioClient
		client, err = activateProcessLoopbackClient()
		if err != nil {
			break
		}
		// The virtual device does not implement GetMixFormat. Request the
		// engine's own rate: the Windows 44.1 kHz conversion of a 48 kHz mix drops
		// samples, which shows up as a slowly drifting, glitching stream. The
		// timeline resamples to AirPlay's 44.1 kHz with sample-accurate timing.
		format := &waveFormatEx{FormatTag: waveFormatIEEEFloat, Channels: 2,
			SamplesPerSec: rate, AvgBytesPerSec: rate * 8,
			BlockAlign: 8, BitsPerSample: 32}
		formatPtr := unsafe.Pointer(format)
		if err = client.initialize(formatPtr, true); err == nil {
			var capture *iAudioCaptureClient
			capture, err = client.getCaptureService()
			if err == nil {
				dbg("[AUDIO] using Windows process loopback at %d Hz", rate)
				return windowsLoopbackCapture{client: client, capture: capture, format: formatPtr, processLoopback: true}, nil
			}
		}
		release(unsafe.Pointer(client))
	}
	dbg("[AUDIO] process loopback unavailable (%v); using endpoint loopback", err)

	client, err := device.activateAudioClient()
	if err != nil {
		return windowsLoopbackCapture{}, err
	}
	formatPtr, err := client.getMixFormat()
	if err != nil {
		release(unsafe.Pointer(client))
		return windowsLoopbackCapture{}, err
	}
	if kind := waveAudioKind(formatPtr); kind != waveFormatPCM && kind != waveFormatIEEEFloat {
		err = fmt.Errorf("unsupported WASAPI mix format: %s", describeWaveFormat(formatPtr))
	} else {
		err = client.initialize(formatPtr, false)
	}
	if err != nil {
		procCoTaskMemFree.Call(uintptr(formatPtr))
		release(unsafe.Pointer(client))
		return windowsLoopbackCapture{}, err
	}
	capture, err := client.getCaptureService()
	if err != nil {
		procCoTaskMemFree.Call(uintptr(formatPtr))
		release(unsafe.Pointer(client))
		return windowsLoopbackCapture{}, err
	}
	return windowsLoopbackCapture{client: client, capture: capture, format: formatPtr, freeFormat: true}, nil
}

// processLoopbackRates returns the capture rates to try, the default render
// endpoint's mix rate first.
func processLoopbackRates(device *iMMDevice) []uint32 {
	rates := []uint32{48000, audioSampleRate}
	client, err := device.activateAudioClient()
	if err != nil {
		return rates
	}
	defer release(unsafe.Pointer(client))
	formatPtr, err := client.getMixFormat()
	if err != nil {
		return rates
	}
	mixRate := (*waveFormatEx)(formatPtr).SamplesPerSec
	procCoTaskMemFree.Call(uintptr(formatPtr))
	if mixRate < 8000 || mixRate > 384000 {
		return rates
	}
	ordered := []uint32{mixRate}
	for _, rate := range rates {
		if rate != mixRate {
			ordered = append(ordered, rate)
		}
	}
	return ordered
}

func coInitialize() (bool, error) {
	hr, _, _ := procCoInitializeEx.Call(0, coinitMultithreaded)
	if uint32(hr) == rpcEChangedMode {
		return false, nil
	}
	if failed(hr) {
		return false, fmt.Errorf("CoInitializeEx: %s", hresultString(hr))
	}
	return true, nil
}

func newMMDeviceEnumerator() (*iMMDeviceEnumerator, error) {
	var ptr unsafe.Pointer
	hr, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidMMDeviceEnum)),
		0,
		clsctxAll,
		uintptr(unsafe.Pointer(&iidIMMDeviceEnumerator)),
		uintptr(unsafe.Pointer(&ptr)),
	)
	if failed(hr) {
		return nil, fmt.Errorf("CoCreateInstance(MMDeviceEnumerator): %s", hresultString(hr))
	}
	return (*iMMDeviceEnumerator)(ptr), nil
}

func (e *iMMDeviceEnumerator) getDefaultAudioEndpoint() (*iMMDevice, error) {
	var ptr unsafe.Pointer
	hr, _, _ := syscall.SyscallN(e.lpVtbl.GetDefaultAudioEndpoint,
		uintptr(unsafe.Pointer(e)),
		eRender,
		eConsole,
		uintptr(unsafe.Pointer(&ptr)),
	)
	if failed(hr) {
		return nil, fmt.Errorf("GetDefaultAudioEndpoint: %s", hresultString(hr))
	}
	return (*iMMDevice)(ptr), nil
}

func (d *iMMDevice) activateAudioClient() (*iAudioClient, error) {
	var ptr unsafe.Pointer
	hr, _, _ := syscall.SyscallN(d.lpVtbl.Activate,
		uintptr(unsafe.Pointer(d)),
		uintptr(unsafe.Pointer(&iidIAudioClient)),
		clsctxAll,
		0,
		uintptr(unsafe.Pointer(&ptr)),
	)
	if failed(hr) {
		return nil, fmt.Errorf("Activate(IAudioClient): %s", hresultString(hr))
	}
	return (*iAudioClient)(ptr), nil
}

func (c *iAudioClient) getMixFormat() (unsafe.Pointer, error) {
	var ptr unsafe.Pointer
	hr, _, _ := syscall.SyscallN(c.lpVtbl.GetMixFormat,
		uintptr(unsafe.Pointer(c)),
		uintptr(unsafe.Pointer(&ptr)),
	)
	if failed(hr) {
		return nil, fmt.Errorf("GetMixFormat: %s", hresultString(hr))
	}
	return ptr, nil
}

func (c *iAudioClient) initialize(format unsafe.Pointer, processLoopback bool) error {
	flags := uintptr(audclntStreamflagsLoopback)
	if processLoopback {
		flags |= audclntStreamflagsAutoconvertPCM
	}
	hr, _, _ := syscall.SyscallN(c.lpVtbl.Initialize,
		uintptr(unsafe.Pointer(c)),
		audclntSharemodeShared,
		flags,
		referenceTime100ms,
		0,
		uintptr(format),
		0,
	)
	if failed(hr) {
		return fmt.Errorf("IAudioClient.Initialize(loopback): %s", hresultString(hr))
	}
	return nil
}

func (c *iAudioClient) getCaptureService() (*iAudioCaptureClient, error) {
	var ptr unsafe.Pointer
	hr, _, _ := syscall.SyscallN(c.lpVtbl.GetService,
		uintptr(unsafe.Pointer(c)),
		uintptr(unsafe.Pointer(&iidIAudioCaptureClient)),
		uintptr(unsafe.Pointer(&ptr)),
	)
	if failed(hr) {
		return nil, fmt.Errorf("GetService(IAudioCaptureClient): %s", hresultString(hr))
	}
	return (*iAudioCaptureClient)(ptr), nil
}

func (c *iAudioClient) start() error {
	hr, _, _ := syscall.SyscallN(c.lpVtbl.Start, uintptr(unsafe.Pointer(c)))
	if failed(hr) {
		return fmt.Errorf("IAudioClient.Start: %s", hresultString(hr))
	}
	return nil
}

func (c *iAudioClient) stop() {
	_, _, _ = syscall.SyscallN(c.lpVtbl.Stop, uintptr(unsafe.Pointer(c)))
}

func (c *iAudioCaptureClient) nextPacketSize() (uint32, error) {
	var frames uint32
	hr, _, _ := syscall.SyscallN(c.lpVtbl.GetNextPacketSize,
		uintptr(unsafe.Pointer(c)),
		uintptr(unsafe.Pointer(&frames)),
	)
	if failed(hr) {
		return 0, fmt.Errorf("GetNextPacketSize: %s", hresultString(hr))
	}
	return frames, nil
}

type capturePacket struct {
	data           unsafe.Pointer
	frames         uint32
	flags          uint32
	devicePosition uint64
	qpcPosition    uint64 // performance counter of the first frame, 100 ns units
}

func (c *iAudioCaptureClient) getBuffer() (capturePacket, error) {
	var packet capturePacket
	hr, _, _ := syscall.SyscallN(c.lpVtbl.GetBuffer,
		uintptr(unsafe.Pointer(c)),
		uintptr(unsafe.Pointer(&packet.data)),
		uintptr(unsafe.Pointer(&packet.frames)),
		uintptr(unsafe.Pointer(&packet.flags)),
		uintptr(unsafe.Pointer(&packet.devicePosition)),
		uintptr(unsafe.Pointer(&packet.qpcPosition)),
	)
	if failed(hr) {
		return capturePacket{}, fmt.Errorf("GetBuffer: %s", hresultString(hr))
	}
	return packet, nil
}

func (c *iAudioCaptureClient) releaseBuffer(frames uint32) error {
	hr, _, _ := syscall.SyscallN(c.lpVtbl.ReleaseBuffer,
		uintptr(unsafe.Pointer(c)),
		uintptr(frames),
	)
	if failed(hr) {
		return fmt.Errorf("ReleaseBuffer: %s", hresultString(hr))
	}
	return nil
}

func decodeWASAPIFrames(data unsafe.Pointer, frames int, formatPtr unsafe.Pointer) []stereoSample {
	format := (*waveFormatEx)(formatPtr)
	out := make([]stereoSample, frames)
	channels := int(format.Channels)
	if channels <= 0 {
		return out
	}
	blockAlign := int(format.BlockAlign)
	if blockAlign <= 0 {
		return out
	}
	bytesPerSample := int(format.BitsPerSample+7) / 8
	if bytesPerSample <= 0 {
		return out
	}
	audioKind := waveAudioKind(formatPtr)
	size := frames * blockAlign
	raw := unsafe.Slice((*byte)(data), size)

	for frame := 0; frame < frames; frame++ {
		base := frame * blockAlign
		left := sampleAt(raw, base, 0, channels, bytesPerSample, audioKind)
		right := left
		if channels > 1 {
			right = sampleAt(raw, base, 1, channels, bytesPerSample, audioKind)
		}
		out[frame] = stereoSample{left: left, right: right}
	}
	return out
}

func sampleAt(raw []byte, frameBase, channel, channels, bytesPerSample int, audioKind uint16) float64 {
	offset := frameBase + channel*bytesPerSample
	if offset+bytesPerSample > len(raw) {
		return 0
	}
	switch audioKind {
	case waveFormatIEEEFloat:
		if bytesPerSample >= 4 {
			bits := binary.LittleEndian.Uint32(raw[offset : offset+4])
			return clampFloat(float64(math.Float32frombits(bits)))
		}
	case waveFormatPCM:
		switch bytesPerSample {
		case 1:
			return (float64(raw[offset]) - 128) / 128
		case 2:
			v := int16(binary.LittleEndian.Uint16(raw[offset : offset+2]))
			return float64(v) / 32768
		case 3:
			v := int32(raw[offset]) | int32(raw[offset+1])<<8 | int32(raw[offset+2])<<16
			if v&0x800000 != 0 {
				v |= ^0xffffff
			}
			return float64(v) / 8388608
		case 4:
			v := int32(binary.LittleEndian.Uint32(raw[offset : offset+4]))
			return float64(v) / 2147483648
		}
	}
	_ = channels
	return 0
}

func (r *audioResampler) resampleToPCM(input []stereoSample) []byte {
	if len(input) == 0 {
		return nil
	}
	if r.inRate <= 0 {
		r.inRate = 44100
	}
	r.buf = append(r.buf, input...)
	step := r.inRate / 44100.0
	if step <= 0 {
		step = 1
	}

	out := make([]byte, 0, len(input)*4)
	for r.phase+1 < float64(len(r.buf)) {
		i := int(r.phase)
		frac := r.phase - float64(i)
		a := r.buf[i]
		b := r.buf[i+1]
		left := a.left + (b.left-a.left)*frac
		right := a.right + (b.right-a.right)*frac
		appendS16Stereo(&out, left, right)
		r.phase += step
	}

	consumed := int(r.phase)
	if consumed > 0 {
		copy(r.buf, r.buf[consumed:])
		r.buf = r.buf[:len(r.buf)-consumed]
		r.phase -= float64(consumed)
	}
	return out
}

func appendS16Stereo(out *[]byte, left, right float64) {
	var tmp [4]byte
	binary.LittleEndian.PutUint16(tmp[0:2], uint16(floatToS16(left)))
	binary.LittleEndian.PutUint16(tmp[2:4], uint16(floatToS16(right)))
	*out = append(*out, tmp[:]...)
}

func floatToS16(v float64) int16 {
	v = clampFloat(v)
	if v >= 1 {
		return math.MaxInt16
	}
	if v <= -1 {
		return math.MinInt16
	}
	return int16(v * 32767)
}

func clampFloat(v float64) float64 {
	if v > 1 {
		return 1
	}
	if v < -1 {
		return -1
	}
	return v
}

func waveAudioKind(formatPtr unsafe.Pointer) uint16 {
	format := (*waveFormatEx)(formatPtr)
	if format.FormatTag != waveFormatExtensibleTag {
		return format.FormatTag
	}
	if format.CbSize < 22 {
		return 0
	}
	ext := (*waveFormatExtensible)(formatPtr)
	if ext.SubFormat.Data1 == waveFormatIEEEFloat {
		return waveFormatIEEEFloat
	}
	if ext.SubFormat.Data1 == waveFormatPCM {
		return waveFormatPCM
	}
	return 0
}

func describeWaveFormat(formatPtr unsafe.Pointer) string {
	format := (*waveFormatEx)(formatPtr)
	kind := waveAudioKind(formatPtr)
	kindName := "unknown"
	switch kind {
	case waveFormatPCM:
		kindName = "pcm"
	case waveFormatIEEEFloat:
		kindName = "float"
	}
	return fmt.Sprintf("%s %d Hz, %d ch, %d bit", kindName, format.SamplesPerSec, format.Channels, format.BitsPerSample)
}

func release(ptr unsafe.Pointer) {
	if ptr == nil {
		return
	}
	unknown := (*struct{ lpVtbl *iUnknownVtbl })(ptr)
	_, _, _ = syscall.SyscallN(unknown.lpVtbl.Release, uintptr(ptr))
}

func failed(hr uintptr) bool {
	return int32(hr) < 0
}

func hresultString(hr uintptr) string {
	return fmt.Sprintf("HRESULT 0x%08x", uint32(hr))
}
