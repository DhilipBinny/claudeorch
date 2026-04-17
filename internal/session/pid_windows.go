//go:build windows

package session

// IsAlive is a stub for Windows (Phase 2). Always returns false so swap is not blocked.
func IsAlive(pid int) bool {
	return false
}
