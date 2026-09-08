//go:build !windows

package airplay

// CheckFFmpeg is a no-op on platforms whose capture backend does not use FFmpeg.
func CheckFFmpeg() error {
	return nil
}
