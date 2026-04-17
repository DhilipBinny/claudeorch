//go:build windows

package main

// stdinIsTerminal always returns false on Windows (Phase 2).
func stdinIsTerminal() bool {
	return false
}
