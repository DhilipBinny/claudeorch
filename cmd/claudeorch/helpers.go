package main

import (
	"os"
	"path/filepath"
	"time"
)

// timeNow returns current UTC time. A var so tests can override it.
var timeNow = func() time.Time { return time.Now().UTC() }

// resolvedExecutable returns os.Executable() with symlinks followed.
// If EvalSymlinks fails (dangling symlink, permission error), falls back
// to the non-resolved path so callers never get an empty string when the
// executable actually exists. Used by upgrade (rename-in-place target),
// uninstall (binary removal target), and statusline install (path
// written to settings.json).
func resolvedExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, evalErr := filepath.EvalSymlinks(exe); evalErr == nil {
		return resolved, nil
	}
	return exe, nil
}
