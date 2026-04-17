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
func IsAlive(pid int) bool {
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
