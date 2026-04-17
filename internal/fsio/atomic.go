// Package fsio provides atomic file writes, POSIX file locks, and permission
// enforcement helpers used throughout claudeorch for credential-safe I/O.
//
// All writes touching credentials or identity files go through WriteFileAtomic,
// which guarantees the target either contains the full new content or the
// original content — never a partial or corrupted state — even on crash or
// power loss.
package fsio

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
)

// ErrCrossDevice is returned when WriteFileAtomic cannot complete because the
// temp file and the target are on different filesystems (rename fails with
// EXDEV on POSIX). Callers should surface this clearly — we do not fall back
// to copy+delete, which loses atomicity.
var ErrCrossDevice = errors.New("fsio: cross-device rename not supported")

// renameFunc is the rename primitive used by WriteFileAtomic.
//
// In production builds it is os.Rename. In the faultinject build (see
// fault_injector.go), it is wrapped with a failure-injecting shim. Keeping
// it as a var allows the swap without #ifdef-style spaghetti.
//
// Unit tests that want to exercise real-rename behavior don't need to touch
// this; tests that want to simulate mid-rename crashes use the faultinject
// build tag.
var renameFunc = os.Rename

// WriteFileAtomic writes data to path atomically.
//
// Strategy (standard crash-safe pattern):
//  1. Create a sibling temp file in the same directory (ensures same-fs rename).
//  2. Write data, fsync, chmod to requested perm, close.
//  3. Rename temp → path (atomic on POSIX same-fs; atomic on NTFS).
//  4. Fsync the parent directory (required on POSIX to durably record the
//     rename metadata — step 3 is atomic for readers but not durable to disk).
//
// Any failure between step 1 and step 3 removes the temp file. Failure after
// step 3 is rare (dir fsync) and logged but doesn't break correctness: the
// file's content is already the new content.
//
// Errors are wrapped with context. ErrCrossDevice is returned if rename
// fails with EXDEV.
//
// Caller responsibilities:
//   - Parent directory must exist. Use EnsureDir first.
//   - perm should be 0600 for credential files, other modes for non-secrets.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	if path == "" {
		return errors.New("fsio.WriteFileAtomic: empty path")
	}
	dir := filepath.Dir(path)
	base := filepath.Base(path)

	// Create temp in same dir. os.CreateTemp uses 0600 by default; we chmod
	// later to match the caller's perm.
	tmp, err := os.CreateTemp(dir, base+".tmp-*")
	if err != nil {
		return fmt.Errorf("fsio.WriteFileAtomic: create temp in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()

	// Any failure after this point removes tmpPath. We deliberately don't
	// use defer because success must NOT remove the renamed-away file.
	removeTmp := func() {
		_ = os.Remove(tmpPath)
	}

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		removeTmp()
		return fmt.Errorf("fsio.WriteFileAtomic: write temp %s: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		removeTmp()
		return fmt.Errorf("fsio.WriteFileAtomic: fsync temp: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		removeTmp()
		return fmt.Errorf("fsio.WriteFileAtomic: chmod temp to %04o: %w", perm, err)
	}
	if err := tmp.Close(); err != nil {
		removeTmp()
		return fmt.Errorf("fsio.WriteFileAtomic: close temp: %w", err)
	}

	if err := renameFunc(tmpPath, path); err != nil {
		removeTmp()
		if isCrossDeviceError(err) {
			return fmt.Errorf("%w: %v (temp in %s, target on different filesystem)", ErrCrossDevice, err, dir)
		}
		return fmt.Errorf("fsio.WriteFileAtomic: rename %s -> %s: %w", tmpPath, path, err)
	}

	// Parent-dir fsync is POSIX-only; on Windows the concept doesn't apply
	// and File.Sync on a directory handle would fail.
	if err := fsyncDir(dir); err != nil {
		// The rename succeeded — data is in place. A dir-fsync failure means
		// the rename might not survive a power loss. Surface as error so
		// callers can decide (most should retry or abort the transaction).
		return fmt.Errorf("fsio.WriteFileAtomic: fsync parent dir %s (data WAS written): %w", dir, err)
	}

	return nil
}

// fsyncDir opens the directory at path and calls Sync on its handle.
// No-op on Windows (directory fsync is a POSIX concept).
func fsyncDir(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// isCrossDeviceError reports whether err is a rename-across-filesystems error
// (EXDEV on Linux/macOS). Uses errno matching rather than string comparison —
// Linux returns "invalid cross-device link", macOS returns "cross-device link",
// both stemming from syscall.EXDEV.
func isCrossDeviceError(err error) bool {
	return errors.Is(err, syscall.EXDEV)
}
