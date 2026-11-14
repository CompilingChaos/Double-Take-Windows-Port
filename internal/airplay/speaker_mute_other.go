//go:build !windows

package airplay

// RestoreInterruptedSpeakerMute is a no-op: only the Windows capture mutes the
// local speakers.
func RestoreInterruptedSpeakerMute() {}
