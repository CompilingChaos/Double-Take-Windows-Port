//go:build windows

package airplay

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CaptureConfig holds screen capture settings.
type CaptureConfig struct {
	FPS        int
	Bitrate    int    // Video bitrate in kbps (0 = auto)
	HWAccel    string // "auto", "nvenc", "none"
	VideoCodec VideoCodec
	MaxWidth   int // receiver-advertised encoded canvas; zero keeps native size
	MaxHeight  int
	ShowCursor bool

	RestoreToken     string
	SaveRestoreToken func(string) error
}

const (
	defaultVideoBitrateKbps = 4500
	minVideoBitrateKbps     = 1800
	maxVideoBitrateKbps     = 12000

	testCaptureWidth  = 1920
	testCaptureHeight = 1080
)

// ScreenCapture manages screen capture through FFmpeg.
type ScreenCapture struct {
	cmd      *exec.Cmd
	stdout   io.ReadCloser
	cancel   context.CancelFunc
	waitCh   chan struct{}
	waitErr  error
	frames   videoAccessUnitReader
	stopOnce sync.Once
}

// StartCapture starts Windows desktop capture through FFmpeg.
func StartCapture(ctx context.Context, cfg CaptureConfig) (*ScreenCapture, error) {
	ffmpegPath, err := ffmpegExecutable()
	if err != nil {
		return nil, err
	}
	return startFFmpegCapture(ctx, cfg, ffmpegPath)
}

func startFFmpegCapture(ctx context.Context, cfg CaptureConfig, ffmpegPath string) (*ScreenCapture, error) {
	fps := cfg.FPS
	if fps <= 0 {
		fps = 30
	}
	bitrate := captureBitrateKbps(cfg)
	keyframeInterval := keyframeIntervalFrames(fps)

	ffArgs := []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-f", "gdigrab",
		"-draw_mouse", ffmpegBoolean(cfg.ShowCursor),
		"-framerate", fmt.Sprintf("%d", fps),
		"-i", "desktop",
		"-an",
	}
	if scale := ffmpegReceiverScaleFilter(cfg); scale != "" {
		ffArgs = append(ffArgs, "-vf", scale)
	}
	encoderArgs, pixelFormat, muxer, err := ffmpegVideoEncoder(cfg, ffmpegPath)
	if err != nil {
		return nil, err
	}
	ffArgs = append(ffArgs, encoderArgs...)
	ffArgs = append(ffArgs,
		"-pix_fmt", pixelFormat,
		"-b:v", fmt.Sprintf("%dk", bitrate),
		"-maxrate", fmt.Sprintf("%dk", bitrate+bitrate/4),
		"-bufsize", fmt.Sprintf("%dk", vbvBufferKbit(bitrate, fps)),
		"-g", fmt.Sprintf("%d", keyframeInterval),
		"-bf", "0",
		"-fflags", "nobuffer",
		"-flags", "low_delay",
		"-flush_packets", "1",
		"-f", muxer,
		"pipe:1",
	)
	capture, err := startFFmpegProcess(ctx, ffmpegPath, ffArgs, "FFMPEG")
	if err == nil && normalizeVideoCodec(cfg.VideoCodec) == VideoCodecHEVC {
		capture.frames = newAnnexBHEVCAccessUnitReader(capture.stdout)
	}
	return capture, err
}

func ffmpegBoolean(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func ffmpegVideoEncoder(cfg CaptureConfig, ffmpegPath string) (args []string, pixelFormat, muxer string, err error) {
	if normalizeVideoCodec(cfg.VideoCodec) != VideoCodecHEVC {
		return ffmpegEncoder(cfg), "yuv420p", "h264", nil
	}

	hwaccel := strings.ToLower(cfg.HWAccel)
	if hwaccel != "none" && ffmpegHEVCHardwareAvailable(ffmpegPath) {
		log.Printf("[CAPTURE] using FFmpeg NVENC HEVC Main10 hardware encoding (hevc_nvenc)")
		return []string{
			"-c:v", "hevc_nvenc",
			"-preset", "p4",
			"-tune", "ull",
			"-rc", "cbr",
			"-zerolatency", "1",
			"-profile:v", "main10",
			"-aud", "1",
			"-repeat_headers", "1",
		}, "p010le", "hevc", nil
	}
	if hwaccel == "nvenc" {
		return nil, "", "", fmt.Errorf("the requested FFmpeg hevc_nvenc encoder is unavailable or failed its hardware probe")
	}
	log.Printf("[CAPTURE] using FFmpeg software HEVC Main10 encoding (libx265)")
	return []string{
		"-c:v", "libx265",
		"-preset", "ultrafast",
		"-tune", "zerolatency",
		"-profile:v", "main10",
		"-x265-params", "repeat-headers=1:aud=1:scenecut=0:bframes=0",
	}, "yuv420p10le", "hevc", nil
}

func ffmpegEncoder(cfg CaptureConfig) []string {
	hwaccel := strings.ToLower(cfg.HWAccel)
	if hwaccel == "nvenc" {
		log.Printf("[CAPTURE] using FFmpeg NVENC hardware encoding (h264_nvenc)")
		return []string{
			"-c:v", "h264_nvenc",
			"-preset", "p4",
			"-tune", "ull",
			"-rc", "cbr",
			"-zerolatency", "1",
		}
	}
	if hwaccel == "auto" {
		dbg("[CAPTURE] FFmpeg auto mode uses libx264; pass -hwaccel nvenc to force NVIDIA encoding")
	}
	log.Printf("[CAPTURE] using FFmpeg software encoding (libx264)")
	return []string{
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-tune", "zerolatency",
		"-x264-params", "repeat-headers=1:scenecut=0",
	}
}

var ffmpegEncoderProbes sync.Map
var ffmpegHEVCHardwareProbes sync.Map

func ffmpegSupportsEncoder(ffmpegPath, encoder string) bool {
	key := ffmpegPath + "\x00" + encoder
	if cached, ok := ffmpegEncoderProbes.Load(key); ok {
		return cached.(bool)
	}
	out, err := exec.Command(ffmpegPath, "-hide_banner", "-encoders").Output()
	found := false
	if err == nil {
		for _, field := range strings.Fields(string(out)) {
			if field == encoder {
				found = true
				break
			}
		}
	}
	ffmpegEncoderProbes.Store(key, found)
	return found
}

func ffmpegHEVCHardwareAvailable(ffmpegPath string) bool {
	if cached, ok := ffmpegHEVCHardwareProbes.Load(ffmpegPath); ok {
		return cached.(bool)
	}
	available := false
	if ffmpegSupportsEncoder(ffmpegPath, "hevc_nvenc") {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, ffmpegPath,
			"-hide_banner", "-loglevel", "error",
			"-f", "lavfi", "-i", "color=size=64x64:rate=1",
			"-frames:v", "1", "-c:v", "hevc_nvenc", "-preset", "p4",
			"-profile:v", "main10", "-pix_fmt", "p010le", "-f", "null", "-")
		available = cmd.Run() == nil
	}
	ffmpegHEVCHardwareProbes.Store(ffmpegPath, available)
	return available
}

// AutomaticHEVCAvailable reports whether Windows has the hardware HEVC path
// used for automatic high-resolution selection. Explicit HEVC may use libx265.
func AutomaticHEVCAvailable(hwaccel string) bool {
	method := strings.ToLower(strings.TrimSpace(hwaccel))
	if method == "none" || (method != "" && method != "auto" && method != "nvenc") {
		return false
	}
	ffmpegPath, err := ffmpegExecutable()
	if err != nil {
		return false
	}
	return ffmpegHEVCHardwareAvailable(ffmpegPath)
}

// ValidateHWAccel checks a Windows capture acceleration value.
func ValidateHWAccel(method string) error {
	switch strings.ToLower(strings.TrimSpace(method)) {
	case "", "auto", "nvenc", "none":
		return nil
	default:
		return fmt.Errorf("unknown hardware acceleration %q (want auto, nvenc, or none)", method)
	}
}

func (sc *ScreenCapture) Read(buf []byte) (int, error) {
	select {
	case <-sc.waitCh:
		if sc.waitErr != nil {
			return 0, fmt.Errorf("capture exited: %w", sc.waitErr)
		}
		return 0, io.EOF
	default:
	}
	return sc.stdout.Read(buf)
}

func (sc *ScreenCapture) ReadVideoAccessUnit() (VideoAccessUnit, error) {
	if sc == nil || sc.frames == nil {
		return VideoAccessUnit{}, fmt.Errorf("timestamped video capture is unavailable")
	}
	return sc.frames.ReadVideoAccessUnit()
}

func (sc *ScreenCapture) Stop() {
	if sc.cmd == nil {
		return
	}
	sc.stopOnce.Do(func() {
		if sc.cancel != nil {
			sc.cancel()
		}
		if sc.stdout != nil {
			sc.stdout.Close()
		}
		if sc.cmd.Process != nil {
			_ = sc.cmd.Process.Signal(os.Interrupt)
		}
		select {
		case <-sc.waitCh:
		case <-time.After(2 * time.Second):
			if sc.cmd.Process != nil {
				_ = sc.cmd.Process.Kill()
			}
			<-sc.waitCh
		}
	})
}

// StartTestCapture creates a synthetic H.264 video stream through FFmpeg.
func StartTestCapture(ctx context.Context, cfg CaptureConfig) (*ScreenCapture, error) {
	ffmpegPath, err := ffmpegExecutable()
	if err != nil {
		return nil, err
	}

	fps := cfg.FPS
	if fps <= 0 {
		fps = 30
	}
	bitrate := captureBitrateKbps(cfg)
	keyframeInterval := keyframeIntervalFrames(fps)

	ffArgs := []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-f", "lavfi",
		"-i", fmt.Sprintf("testsrc2=size=%dx%d:rate=%d", testCaptureWidth, testCaptureHeight, fps),
		"-an",
	}
	if scale := ffmpegReceiverScaleFilter(cfg); scale != "" {
		ffArgs = append(ffArgs, "-vf", scale)
	}
	encoderArgs, pixelFormat, muxer, err := ffmpegVideoEncoder(cfg, ffmpegPath)
	if err != nil {
		return nil, err
	}
	ffArgs = append(ffArgs, encoderArgs...)
	ffArgs = append(ffArgs,
		"-pix_fmt", pixelFormat,
		"-b:v", fmt.Sprintf("%dk", bitrate),
		"-maxrate", fmt.Sprintf("%dk", bitrate+bitrate/4),
		"-bufsize", fmt.Sprintf("%dk", vbvBufferKbit(bitrate, fps)),
		"-g", fmt.Sprintf("%d", keyframeInterval),
		"-bf", "0",
		"-f", muxer,
		"pipe:1",
	)
	capture, err := startFFmpegProcess(ctx, ffmpegPath, ffArgs, "FFMPEG")
	if err == nil && normalizeVideoCodec(cfg.VideoCodec) == VideoCodecHEVC {
		capture.frames = newAnnexBHEVCAccessUnitReader(capture.stdout)
	}
	return capture, err
}

func startFFmpegProcess(ctx context.Context, name string, args []string, logPrefix string) (*ScreenCapture, error) {
	captureCtx, cancel := context.WithCancel(ctx)
	dbg("[CAPTURE] %s %s", name, strings.Join(args, " "))
	cmd := exec.CommandContext(captureCtx, name, args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("%s stdout pipe: %w", name, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("%s stderr pipe: %w", name, err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start %s: %w", name, err)
	}
	go logStderr(logPrefix, stderr)

	capture := &ScreenCapture{
		cmd:    cmd,
		stdout: stdout,
		cancel: cancel,
		waitCh: make(chan struct{}),
	}
	go func() {
		capture.waitErr = cmd.Wait()
		close(capture.waitCh)
	}()
	return capture, nil
}

func logStderr(prefix string, r io.Reader) {
	if r == nil {
		return
	}
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		dbg("[%s] %s", prefix, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		dbg("[%s] stderr read error: %v", prefix, err)
	}
}

func captureBitrateKbps(cfg CaptureConfig) int {
	if cfg.Bitrate > 0 {
		return cfg.Bitrate
	}
	fps := cfg.FPS
	if fps <= 0 {
		fps = 30
	}
	width, height := 1920, 1080
	if maxWidth, maxHeight := receiverCaptureSize(cfg); maxWidth > 0 && maxWidth*maxHeight < width*height {
		width, height = maxWidth, maxHeight
	}
	bitrate := recommendedBitrateKbps(width, height, fps)
	log.Printf("[CAPTURE] auto bitrate selected: %d kbps for %dx%d@%dfps", bitrate, width, height, fps)
	return bitrate
}

func recommendedBitrateKbps(width, height, fps int) int {
	if width <= 0 || height <= 0 || fps <= 0 {
		return defaultVideoBitrateKbps
	}
	bitrate := (width*height*fps + 7500) / 15000
	if bitrate < minVideoBitrateKbps {
		return minVideoBitrateKbps
	}
	if bitrate > maxVideoBitrateKbps {
		return maxVideoBitrateKbps
	}
	return bitrate
}

func keyframeIntervalFrames(fps int) int {
	if fps <= 0 {
		fps = 30
	}
	return fps * 4
}

func vbvBufferKbit(bitrateKbps, fps int) int {
	if bitrateKbps <= 0 || fps <= 0 {
		return 300
	}
	vbv := bitrateKbps * 2 / fps
	if vbv < 200 {
		return 200
	}
	return vbv
}

func detectPrimaryMonitor(display string) (startX, startY, endX, endY int) {
	return 0, 0, 0, 0
}

func parseXrandrGeometry(line string) (xOffset, yOffset, width, height int, ok bool) {
	for _, field := range strings.Fields(line) {
		parts := strings.SplitN(field, "x", 2)
		if len(parts) != 2 {
			continue
		}
		w, err := strconv.Atoi(parts[0])
		if err != nil || w < 640 {
			continue
		}
		rest := parts[1]
		plusParts := strings.SplitN(rest, "+", 3)
		if len(plusParts) != 3 {
			continue
		}
		h, err := strconv.Atoi(plusParts[0])
		if err != nil {
			continue
		}
		x, err := strconv.Atoi(plusParts[1])
		if err != nil {
			continue
		}
		y, err := strconv.Atoi(plusParts[2])
		if err != nil {
			continue
		}
		return x, y, w, h, true
	}
	return 0, 0, 0, 0, false
}
