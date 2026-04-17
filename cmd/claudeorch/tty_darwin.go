//go:build darwin

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

// fdIsTerminal reports whether the given fd refers to an interactive terminal.
// macOS (and other BSDs) use the TIOCGETA termios-get ioctl, not Linux's
// TCGETS. Functionally equivalent: /dev/null and pipes both fail it while
// a real PTY succeeds.
func fdIsTerminal(fd uintptr) bool {
	_, err := unix.IoctlGetTermios(int(fd), unix.TIOCGETA)
	return err == nil
}

func stdinIsTerminal() bool  { return fdIsTerminal(os.Stdin.Fd()) }
func stderrIsTerminal() bool { return fdIsTerminal(os.Stderr.Fd()) }
