package airplay

import "fmt"

func receiverCaptureSize(cfg CaptureConfig) (int, int) {
	width, height := cfg.MaxWidth&^1, cfg.MaxHeight&^1
	if width <= 0 || height <= 0 {
		return 0, 0
	}
	return width, height
}

func ffmpegReceiverScaleFilter(cfg CaptureConfig) string {
	width, height := receiverCaptureSize(cfg)
	if width == 0 || height == 0 {
		return ""
	}
	return fmt.Sprintf(
		"scale=w=%d:h=%d:force_original_aspect_ratio=decrease:force_divisible_by=2,pad=%d:%d:(ow-iw)/2:(oh-ih)/2",
		width, height, width, height,
	)
}
