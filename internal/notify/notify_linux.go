//go:build linux

package notify

import (
	"os/exec"
)

// Send sends a Linux desktop notification via notify-send.
func Send(title, message string) error {
	return exec.Command("notify-send", "--app-name=claudeorch", title, message).Run()
}
