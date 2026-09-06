//go:build windows

package notify

// Send is a no-op on Windows (tmux not supported).
func Send(title, message string) error {
	return nil
}
