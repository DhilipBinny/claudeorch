//go:build darwin

package session

// ConfigDirForPID returns the value of CLAUDE_CONFIG_DIR in the given
// process's environment. macOS support is not yet implemented (requires
// sysctl KERN_PROCARGS2 parsing). Returns "" so callers treat the profile
// as unknown.
func ConfigDirForPID(pid int) string {
	_ = pid
	return ""
}
