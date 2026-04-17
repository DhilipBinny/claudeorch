package fsio

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

// ErrLockTimeout is returned when AcquireLock cannot obtain the exclusive lock
// within the deadline.
var ErrLockTimeout = errors.New("fsio: timed out waiting for file lock")

// LockTimeout is the maximum time AcquireLock will wait before giving up.
const LockTimeout = 3 * time.Second

// AcquireLock opens path (creating it if absent), acquires an exclusive POSIX
// flock, and returns a release function.
//
// The file descriptor is kept open for the duration of the lock — flock is
// fd-bound, not path-bound, so closing the fd releases the lock immediately.
// Callers must call release() when the protected section ends.
//
// If the lock cannot be acquired within LockTimeout, ErrLockTimeout is
// returned and release is nil.
//
// Caller responsibilities:
//   - path's parent directory must exist.
//   - Do not call release() more than once.
func AcquireLock(ctx context.Context, path string) (release func() error, err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("fsio.AcquireLock: open %s: %w", path, err)
	}

	// Derive deadline from context + per-call timeout; use whichever is sooner.
	deadline := time.Now().Add(LockTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}

	if err := acquireExclusiveFlock(f, deadline); err != nil {
		_ = f.Close()
		if errors.Is(err, ErrLockTimeout) {
			return nil, ErrLockTimeout
		}
		return nil, fmt.Errorf("fsio.AcquireLock: flock %s: %w", path, err)
	}

	release = func() error {
		if releaseErr := releaseFlock(f); releaseErr != nil {
			_ = f.Close()
			return fmt.Errorf("fsio.AcquireLock: release flock %s: %w", path, releaseErr)
		}
		return f.Close()
	}
	return release, nil
}
