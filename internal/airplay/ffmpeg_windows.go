//go:build windows

package airplay

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

var (
	ffmpegOnce sync.Once
	ffmpegPath string
	ffmpegErr  error
)

// CheckFFmpeg verifies that Windows screen capture can start. FFmpeg beside
// doubletake.exe takes priority over an installation found through PATH.
func CheckFFmpeg() error {
	_, err := ffmpegExecutable()
	return err
}

func ffmpegExecutable() (string, error) {
	ffmpegOnce.Do(func() {
		executablePath, executableErr := os.Executable()
		resolvedPath, bundled, err := locateFFmpeg(executablePath, exec.LookPath)
		if err != nil {
			if executableErr != nil {
				ffmpegErr = fmt.Errorf("%w (the application directory could not be determined: %v)", err, executableErr)
			} else {
				ffmpegErr = err
			}
			return
		}
		ffmpegPath = resolvedPath
		if bundled {
			log.Printf("[SETUP] FFmpeg found next to doubletake.exe: %s", ffmpegPath)
		} else {
			log.Printf("[SETUP] FFmpeg found on PATH: %s", ffmpegPath)
		}
	})
	return ffmpegPath, ffmpegErr
}

func locateFFmpeg(executablePath string, lookPath func(string) (string, error)) (string, bool, error) {
	var bundledPath string
	if executablePath != "" {
		bundledPath = filepath.Join(filepath.Dir(executablePath), "ffmpeg.exe")
		if info, err := os.Stat(bundledPath); err == nil && !info.IsDir() {
			return bundledPath, true, nil
		}
	}

	if path, err := lookPath("ffmpeg"); err == nil {
		return path, false, nil
	}

	if bundledPath != "" {
		return "", false, fmt.Errorf("FFmpeg was not found. Put ffmpeg.exe in the same directory as doubletake.exe:\n  %s\nThen run doubletake.exe again. Alternatively, install FFmpeg and add it to PATH", bundledPath)
	}
	return "", false, fmt.Errorf("FFmpeg was not found. Put ffmpeg.exe in the same directory as doubletake.exe, or install FFmpeg and add it to PATH")
}
