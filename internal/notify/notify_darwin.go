//go:build darwin

package notify

import (
	"os/exec"
)

// Send sends a macOS notification via osascript.
func Send(title, message string) error {
	script := `display notification "` + escapeAppleScript(message) + `" with title "` + escapeAppleScript(title) + `" sound name "Glass"`
	return exec.Command("osascript", "-e", script).Run()
}

func escapeAppleScript(s string) string {
	var out []byte
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			out = append(out, '\\', '"')
		case '\\':
			out = append(out, '\\', '\\')
		default:
			out = append(out, s[i])
		}
	}
	return string(out)
}
