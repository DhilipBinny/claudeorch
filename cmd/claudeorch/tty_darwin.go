//go:build darwin

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

// stdinIsTerminal reports whether os.Stdin is a real interactive terminal.
// On macOS (and other BSDs) the termios-get ioctl request is TIOCGETA, not
// Linux's TCGETS. Functionally equivalent: /dev/null and pipes both fail
// this ioctl while a real PTY succeeds.
func stdinIsTerminal() bool {
	_, err := unix.IoctlGetTermios(int(os.Stdin.Fd()), unix.TIOCGETA)
	return err == nil
}
