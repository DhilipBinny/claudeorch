//go:build linux

package session

import (
	"bytes"
	"fmt"
	"os"
)

// ConfigDirForPID returns the value of CLAUDE_CONFIG_DIR in the given
// process's environment, or "" if unset or unreadable.
//
// Linux exposes /proc/<pid>/environ as NUL-separated KEY=VALUE pairs.
// Same-user processes are readable without special privileges; for other
// users we'd get EACCES and return "" (which callers treat as "unknown").
func ConfigDirForPID(pid int) string {
	if pid <= 0 {
		return ""
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		return ""
	}
	const prefix = "CLAUDE_CONFIG_DIR="
	for _, entry := range bytes.Split(data, []byte{0}) {
		if bytes.HasPrefix(entry, []byte(prefix)) {
			return string(entry[len(prefix):])
		}
	}
	return ""
}
