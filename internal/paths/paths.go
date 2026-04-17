// Package paths resolves filesystem locations for Claude Code and claudeorch.
//
// Every function is pure (no I/O): it only reads environment variables and
// returns string paths. Callers are responsible for creating directories or
// checking existence.
//
// Two env vars alter resolution:
//
//   - CLAUDE_CONFIG_DIR overrides Claude Code's default "~/.claude". When set,
//     it ALSO changes where .claude.json lives (inside the config dir, not at
//     HOME). This asymmetry matches Claude Code's own resolution and is the
//     single trickiest part of this package.
//   - CLAUDEORCH_HOME overrides claudeorch's default "~/.claudeorch" (for
//     testing and advanced users).
//
// All functions return error if $HOME is required and unset. We never fabricate
// a fallback — a user without HOME has bigger problems and should see a loud
// error, not a silent /tmp write.
package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// ErrNoHome is returned when $HOME is required but not set in the environment.
var ErrNoHome = errors.New("$HOME environment variable not set")

// ErrInvalidName is returned when a profile name fails validation.
var ErrInvalidName = errors.New("invalid profile name")

// profileNamePattern accepts names that are safe for filesystem paths, shell
// arguments, and tab-completion:
//
//   - Must start with an alphanumeric character.
//   - Remaining characters may be alphanumeric, hyphen, or underscore.
//   - 1 to 64 characters total.
//
// This rejects ".", "..", leading dots (hidden), spaces, slashes, control
// characters, and path-traversal patterns. Strict by design — users can
// always rename to something prettier if they want, and loose names cause
// user-visible bugs later (shell globbing, path injection, case collisions
// on macOS).
var profileNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// ValidateProfileName returns nil if the name is acceptable, or
// ErrInvalidName wrapped with context otherwise.
func ValidateProfileName(name string) error {
	if !profileNamePattern.MatchString(name) {
		return fmt.Errorf("%w: %q (must be 1-64 chars, alphanumeric + hyphen/underscore, starting with alphanumeric)", ErrInvalidName, name)
	}
	return nil
}

// home returns $HOME or ErrNoHome.
func home() (string, error) {
	h := os.Getenv("HOME")
	if h == "" {
		return "", ErrNoHome
	}
	return h, nil
}

// ClaudeConfigHome returns the directory Claude Code uses as its config root.
//
// Resolution:
//  1. If $CLAUDE_CONFIG_DIR is set (even empty string counts as unset) → that value.
//  2. Else → $HOME/.claude.
//
// Empty-string CLAUDE_CONFIG_DIR is treated as unset rather than as path "".
// This matches how Claude Code itself behaves — setting the variable to empty
// does not mean "use empty path," it means "use default."
func ClaudeConfigHome() (string, error) {
	if v := os.Getenv("CLAUDE_CONFIG_DIR"); v != "" {
		return v, nil
	}
	h, err := home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".claude"), nil
}

// ClaudeJSONPath returns the path to .claude.json (account identity + metadata).
//
// Critical asymmetry:
//   - When CLAUDE_CONFIG_DIR is SET → <CLAUDE_CONFIG_DIR>/.claude.json (inside the dir).
//   - When CLAUDE_CONFIG_DIR is UNSET → $HOME/.claude.json (at homedir, NOT inside .claude/).
//
// This exactly mirrors Claude Code's internal path resolution. Getting it wrong
// is a major footgun: a naive "always inside config home" would fail to find
// the real file and prompt re-login.
func ClaudeJSONPath() (string, error) {
	if v := os.Getenv("CLAUDE_CONFIG_DIR"); v != "" {
		return filepath.Join(v, ".claude.json"), nil
	}
	h, err := home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".claude.json"), nil
}

// ClaudeCredentialsPath returns the path to .credentials.json (OAuth tokens).
//
// Always inside ClaudeConfigHome(), regardless of env-var state.
func ClaudeCredentialsPath() (string, error) {
	home, err := ClaudeConfigHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".credentials.json"), nil
}

// ClaudeSessionsDir returns the directory holding session PID files
// (<config_home>/sessions/*.json).
func ClaudeSessionsDir() (string, error) {
	home, err := ClaudeConfigHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "sessions"), nil
}

// ClaudeIDEDir returns the directory holding IDE lockfiles
// (<config_home>/ide/*.lock).
func ClaudeIDEDir() (string, error) {
	home, err := ClaudeConfigHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "ide"), nil
}

// ClaudeorchHome returns the claudeorch state directory.
//
// Resolution:
//  1. If $CLAUDEORCH_HOME is set to a non-empty value → that value.
//  2. Else → $HOME/.claudeorch.
//
// Mode 0700 enforcement happens in the fsio package, not here.
func ClaudeorchHome() (string, error) {
	if v := os.Getenv("CLAUDEORCH_HOME"); v != "" {
		return v, nil
	}
	h, err := home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".claudeorch"), nil
}

// StoreFile returns the path to store.json (profile metadata index).
func StoreFile() (string, error) {
	home, err := ClaudeorchHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "store.json"), nil
}

// ProfilesRoot returns <claudeorch_home>/profiles (container for all profiles).
func ProfilesRoot() (string, error) {
	home, err := ClaudeorchHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "profiles"), nil
}

// ProfileDir returns <claudeorch_home>/profiles/<name>.
//
// The name is validated via ValidateProfileName before path construction,
// preventing path traversal (e.g., "../../etc/passwd") or invalid names.
func ProfileDir(name string) (string, error) {
	if err := ValidateProfileName(name); err != nil {
		return "", err
	}
	root, err := ProfilesRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, name), nil
}

// IsolatesRoot returns <claudeorch_home>/isolate (container for materialized
// CLAUDE_CONFIG_DIR targets used by `claudeorch launch`).
func IsolatesRoot() (string, error) {
	home, err := ClaudeorchHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "isolate"), nil
}

// IsolateDir returns <claudeorch_home>/isolate/<name>.
// Validates the name. This directory is what we set CLAUDE_CONFIG_DIR to when
// launching Claude with a specific profile in isolate mode.
func IsolateDir(name string) (string, error) {
	if err := ValidateProfileName(name); err != nil {
		return "", err
	}
	root, err := IsolatesRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, name), nil
}

// LockFile returns the path to the single global file lock.
// Held during every state-mutating command — see internal/fsio Lock.
func LockFile() (string, error) {
	home, err := ClaudeorchHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "locks", ".lock"), nil
}

// LogDir returns <claudeorch_home>/log (parent for rotated log files).
func LogDir() (string, error) {
	home, err := ClaudeorchHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "log"), nil
}

// LogFile returns the primary log file path (lumberjack rotates alongside it).
func LogFile() (string, error) {
	dir, err := LogDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "claudeorch.log"), nil
}

// TmpSwapDir returns a PID-qualified staging directory for in-flight swap
// operations. Left on disk until the swap completes (success) or is recovered
// (crash). See the swap package for lifecycle.
//
// PID < 1 is rejected because 0 means "invalid PID" and negative is never a
// real PID.
func TmpSwapDir(pid int) (string, error) {
	if pid < 1 {
		return "", fmt.Errorf("TmpSwapDir: invalid pid %d (must be > 0)", pid)
	}
	home, err := ClaudeorchHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, fmt.Sprintf("tmp-swap-%d", pid)), nil
}
