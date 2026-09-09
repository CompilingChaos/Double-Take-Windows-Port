package airplay

import "os/exec"

func hasGstElement(name string) bool {
	return exec.Command("gst-inspect-1.0", name).Run() == nil
}
