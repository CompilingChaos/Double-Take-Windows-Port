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
	FPS       int
	Bitrate   int    // Video bitrate in kbps (0 = auto)
	HWAccel   string // "auto", "nvenc", "none"
	MaxWidth  int    // receiver-advertised encoded canvas; zero keeps native size
	MaxHeight int

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
		"-draw_mouse", "1",
		"-framerate", fmt.Sprintf("%d", fps),
		"-i", "desktop",
		"-an",
	}
	if scale := ffmpegReceiverScaleFilter(cfg); scale != "" {
		ffArgs = append(ffArgs, "-vf", scale)
	}
	ffArgs = append(ffArgs, ffmpegEncoder(cfg)...)
	ffArgs = append(ffArgs,
		"-pix_fmt", "yuv420p",
		"-b:v", fmt.Sprintf("%dk", bitrate),
		"-maxrate", fmt.Sprintf("%dk", bitrate+bitrate/4),
		"-bufsize", fmt.Sprintf("%dk", vbvBufferKbit(bitrate, fps)),
		"-g", fmt.Sprintf("%d", keyframeInterval),
		"-bf", "0",
		"-fflags", "nobuffer",
		"-flags", "low_delay",
		"-flush_packets", "1",
		"-f", "h264",
		"pipe:1",
	)
	return startFFmpegProcess(ctx, ffmpegPath, ffArgs, "FFMPEG")
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
	ffArgs = append(ffArgs,
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-tune", "zerolatency",
		"-x264-params", "repeat-headers=1:scenecut=0",
		"-pix_fmt", "yuv420p",
		"-b:v", fmt.Sprintf("%dk", bitrate),
		"-maxrate", fmt.Sprintf("%dk", bitrate+bitrate/4),
		"-bufsize", fmt.Sprintf("%dk", vbvBufferKbit(bitrate, fps)),
		"-g", fmt.Sprintf("%d", keyframeInterval),
		"-bf", "0",
		"-f", "h264",
		"pipe:1",
	)
	return startFFmpegProcess(ctx, ffmpegPath, ffArgs, "FFMPEG")
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
