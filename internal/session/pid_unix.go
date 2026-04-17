//go:build !windows

package session

import (
	"errors"
	"syscall"
)

// IsAlive reports whether the process with the given PID is alive.
//
// Uses kill(pid, 0) — the canonical POSIX liveness check:
//   - ESRCH:  no such process → dead
//   - EPERM:  process exists but we lack permission to signal it → alive
//   - nil:    process exists and we can signal it → alive
//   - other:  treat as alive (be conservative — don't block valid operations)
//
// Defensive: pid ≤ 0 is always treated as dead. kill(0, 0) signals the
// caller's process group and kill(-1, 0) signals every signallable process —
// neither is a meaningful liveness check and both would silently return true.
func IsAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	if errors.Is(err, syscall.ESRCH) {
		return false
	}
	// EPERM or any other error: process exists.
	return true
}
