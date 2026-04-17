//go:build !windows

package fsio

import (
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// acquireExclusiveFlock tries to acquire an exclusive POSIX flock on f,
// polling until deadline. We use non-blocking LOCK_EX|LOCK_NB and retry,
// rather than blocking LOCK_EX, so the deadline is respected even when a
// different process holds the lock.
func acquireExclusiveFlock(f *os.File, deadline time.Time) error {
	const pollInterval = 50 * time.Millisecond
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return nil
		}
		if err != unix.EWOULDBLOCK {
			return err
		}
		if time.Now().After(deadline) {
			return ErrLockTimeout
		}
		time.Sleep(pollInterval)
	}
}

// releaseFlock releases the flock held by f.
func releaseFlock(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
