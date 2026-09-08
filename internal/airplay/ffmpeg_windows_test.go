//go:build windows

package airplay

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocateFFmpegPrefersExecutableDirectory(t *testing.T) {
	dir := t.TempDir()
	executablePath := filepath.Join(dir, "doubletake.exe")
	bundledPath := filepath.Join(dir, "ffmpeg.exe")
	if err := os.WriteFile(bundledPath, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}

	lookPathCalled := false
	got, bundled, err := locateFFmpeg(executablePath, func(string) (string, error) {
		lookPathCalled = true
		return `C:\ffmpeg\bin\ffmpeg.exe`, nil
	})
	if err != nil {
		t.Fatalf("locateFFmpeg() error = %v", err)
	}
	if got != bundledPath || !bundled {
		t.Fatalf("locateFFmpeg() = (%q, %v), want (%q, true)", got, bundled, bundledPath)
	}
	if lookPathCalled {
		t.Fatal("PATH was searched even though bundled ffmpeg.exe exists")
	}
}

func TestLocateFFmpegFallsBackToPATH(t *testing.T) {
	want := `C:\ffmpeg\bin\ffmpeg.exe`
	got, bundled, err := locateFFmpeg(filepath.Join(t.TempDir(), "doubletake.exe"), func(name string) (string, error) {
		if name != "ffmpeg" {
			t.Fatalf("LookPath name = %q, want ffmpeg", name)
		}
		return want, nil
	})
	if err != nil {
		t.Fatalf("locateFFmpeg() error = %v", err)
	}
	if got != want || bundled {
		t.Fatalf("locateFFmpeg() = (%q, %v), want (%q, false)", got, bundled, want)
	}
}

func TestLocateFFmpegMissingErrorIsActionable(t *testing.T) {
	dir := t.TempDir()
	executablePath := filepath.Join(dir, "doubletake.exe")
	_, _, err := locateFFmpeg(executablePath, func(string) (string, error) {
		return "", errors.New("not found")
	})
	if err == nil {
		t.Fatal("locateFFmpeg() error = nil, want missing FFmpeg error")
	}
	for _, want := range []string{"FFmpeg was not found", "same directory as doubletake.exe", filepath.Join(dir, "ffmpeg.exe"), "PATH"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}
