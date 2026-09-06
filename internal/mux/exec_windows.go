//go:build windows

package mux

import (
	"fmt"
)

func execSyscall(path string, argv []string) error {
	return fmt.Errorf("tmux is not supported on Windows")
}
