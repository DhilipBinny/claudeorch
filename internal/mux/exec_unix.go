//go:build !windows

package mux

import "syscall"

func execSyscall(path string, argv []string) error {
	return syscall.Exec(path, argv, syscall.Environ())
}
