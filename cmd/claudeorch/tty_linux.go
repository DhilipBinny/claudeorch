//go:build linux

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

// stdinIsTerminal reports whether os.Stdin is a real interactive terminal.
// On Linux the termios-get ioctl request is TCGETS; /dev/null and pipes
// both fail this ioctl while a real PTY succeeds.
func stdinIsTerminal() bool {
	_, err := unix.IoctlGetTermios(int(os.Stdin.Fd()), unix.TCGETS)
	return err == nil
}
