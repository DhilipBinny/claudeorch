//go:build linux

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

// fdIsTerminal reports whether the given fd refers to an interactive terminal.
// Linux uses the TCGETS termios-get ioctl; /dev/null and pipes both fail it
// while a real PTY succeeds.
func fdIsTerminal(fd uintptr) bool {
	_, err := unix.IoctlGetTermios(int(fd), unix.TCGETS)
	return err == nil
}

func stdinIsTerminal() bool  { return fdIsTerminal(os.Stdin.Fd()) }
func stderrIsTerminal() bool { return fdIsTerminal(os.Stderr.Fd()) }
