package fsio

import (
	"fmt"
	"os"
)

// EnsureDir creates dir (and all parents) with mode if it doesn't exist.
// If it already exists and is a directory, the call is a no-op (existing
// permissions are not changed — use CheckPerms to report drift).
func EnsureDir(path string, mode os.FileMode) error {
	if err := os.MkdirAll(path, mode); err != nil {
		return fmt.Errorf("fsio.EnsureDir: %s: %w", path, err)
	}
	return nil
}

// EnsureFile creates path with the given mode if it does not exist.
// If path already exists as a regular file, it is a no-op.
// Returns an error if path exists but is not a regular file.
func EnsureFile(path string, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		if os.IsExist(err) {
			// File already exists — verify it is a regular file.
			info, statErr := os.Stat(path)
			if statErr != nil {
				return fmt.Errorf("fsio.EnsureFile: stat %s: %w", path, statErr)
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("fsio.EnsureFile: %s exists but is not a regular file", path)
			}
			return nil
		}
		return fmt.Errorf("fsio.EnsureFile: create %s: %w", path, err)
	}
	return f.Close()
}

// CheckPerms reports whether path has exactly the expected permission bits.
// It is intentionally report-only: it never modifies permissions. Callers
// that want to fix drift should do so explicitly (e.g., os.Chmod).
//
// Returns nil if the permissions match. Returns a descriptive error if they
// don't (suitable for surfacing in `doctor` output).
func CheckPerms(path string, want os.FileMode) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("fsio.CheckPerms: stat %s: %w", path, err)
	}
	got := info.Mode().Perm()
	if got != want {
		return fmt.Errorf("fsio.CheckPerms: %s has mode %04o, want %04o", path, got, want)
	}
	return nil
}
