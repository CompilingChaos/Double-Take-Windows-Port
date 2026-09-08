//go:build !windows

package airplay

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// StartAudioCapture launches a pipeline that captures system audio and feeds
// raw PCM into the encoder negotiated with the receiver.
func StartAudioCapture(ctx context.Context, testTone bool, codec AudioCodec) (*AudioCapture, error) {
	captureCtx, cancel := context.WithCancel(ctx)
	if codec != AudioCodecALAC && codec != AudioCodecAACELD {
		cancel()
		return nil, fmt.Errorf("unsupported audio codec %d", codec)
	}
	_, codecSPF, _, _, _, _ := codec.Info()

	var srcArgs []string
	if testTone {
		srcArgs = []string{"audiotestsrc", "wave=sine", "freq=440", "is-live=true",
			fmt.Sprintf("samplesperbuffer=%d", codecSPF)}
		dbg("[AUDIO] using test tone (440 Hz sine wave, live, spf=%d)", codecSPF)
	} else if exec.Command("gst-inspect-1.0", "pulsesrc").Run() == nil {
		monitor := detectPulseMonitor()
		if monitor == "" {
			cancel()
			return nil, fmt.Errorf("no PulseAudio monitor source found")
		}
		srcArgs = []string{"pulsesrc", fmt.Sprintf("device=%s", monitor)}
		dbg("[AUDIO] using pulsesrc device=%s", monitor)
	} else if exec.Command("gst-inspect-1.0", "pipewiresrc").Run() == nil {
		srcArgs = []string{"pipewiresrc"}
		dbg("[AUDIO] using pipewiresrc")
	} else {
		cancel()
		return nil, fmt.Errorf("no audio source available (need pulsesrc or pipewiresrc)")
	}

	ac := &AudioCapture{
		cancel: cancel,
		waitCh: make(chan struct{}),
		codec:  codec,
	}
	if codec == AudioCodecAACELD {
		var err error
		ac.eld, err = newELDEncoder()
		if err != nil {
			cancel()
			return nil, err
		}
	}

	gstArgs := []string{"--quiet"}
	gstArgs = append(gstArgs, srcArgs...)
	gstArgs = append(gstArgs,
		"!", "audioconvert",
		"!", "audioresample",
		"!", "audio/x-raw,rate=44100,channels=2,format=S16LE",
		"!", "queue", "max-size-buffers=2", "max-size-bytes=0", "max-size-time=0", "leaky=downstream",
		"!", "fdsink", "fd=1", "sync=false", "async=false",
	)
	dbg("[AUDIO] PCM capture pipeline: gst-launch-1.0 %s", strings.Join(gstArgs, " "))

	gstCmd := exec.CommandContext(captureCtx, "gst-launch-1.0", gstArgs...)
	gstStdout, err := gstCmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("gst stdout pipe: %w", err)
	}
	gstStderr, _ := gstCmd.StderrPipe()

	if err := gstCmd.Start(); err != nil {
		if ac.eld != nil {
			ac.eld.Close()
			ac.eld = nil
		}
		cancel()
		return nil, fmt.Errorf("start audio capture pipeline: %w", err)
	}
	go logStderr("AUDIO-GST", gstStderr)

	ac.stopFn = func() {
		if gstCmd.Process != nil {
			_ = gstCmd.Process.Kill()
		}
	}
	ac.pcmPipe = gstStdout
	go func() {
		ac.waitErr = gstCmd.Wait()
		close(ac.waitCh)
	}()

	return ac, nil
}

func detectPulseMonitor() string {
	out, err := exec.Command("pactl", "get-default-sink").Output()
	if err != nil {
		dbg("[AUDIO] pactl get-default-sink failed: %v", err)
		return ""
	}
	sinkName := strings.TrimSpace(string(out))
	if sinkName == "" {
		return ""
	}
	return sinkName + ".monitor"
}
