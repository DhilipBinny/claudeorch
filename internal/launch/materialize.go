// Package launch handles creation of per-profile isolate directories and
// exec-ing into Claude Code with the right CLAUDE_CONFIG_DIR.
package launch

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/DhilipBinny/claudeorch/internal/fsio"
)

// sharedSymlinks is the set of files/dirs from the default Claude config home
// that are symlinked (shared) into isolate dirs. settings.local.json is
// excluded because it can contain per-session state.
var sharedSymlinks = []string{
	"CLAUDE.md",
	"projects",
	"skills",
	"settings.json",
	"statusline-command.sh",
}

// Materialize creates (or updates) the isolate directory for the given profile.
//
// Layout:
//   - isolateDir/credentials.json  — 0600 copy from profileDir
//   - isolateDir/.claude.json      — 0600 copy from profileDir
//   - isolateDir/CLAUDE.md         — symlink to claudeConfigHome/CLAUDE.md (if it exists)
//   - isolateDir/projects/         — symlink to claudeConfigHome/projects/
//   - isolateDir/skills/           — symlink to claudeConfigHome/skills/
//   - isolateDir/settings.json     — symlink to claudeConfigHome/settings.json
//   - settings.local.json          — copied (not symlinked) to avoid leaking session state
//
// When isolated=true, no symlinks are created (fully isolated session).
// Broken symlinks are repaired on each call (idempotent).
// Returns the path to the isolateDir.
func Materialize(profileDir, isolateDir, claudeConfigHome string, isolated bool) (string, error) {
	if err := fsio.EnsureDir(isolateDir, 0o700); err != nil {
		return "", fmt.Errorf("launch.Materialize: %w", err)
	}

	// Copy credentials.
	if err := copyCredentials(profileDir, isolateDir); err != nil {
		return "", err
	}

	// Copy claude.json.
	if err := copyFileToDir(filepath.Join(profileDir, "claude.json"),
		filepath.Join(isolateDir, ".claude.json"), 0o600); err != nil {
		return "", fmt.Errorf("launch.Materialize: copy .claude.json: %w", err)
	}

	// Copy settings.local.json if it exists (not shared).
	localSettings := filepath.Join(claudeConfigHome, "settings.local.json")
	if _, err := os.Stat(localSettings); err == nil {
		if err := copyFileToDir(localSettings,
			filepath.Join(isolateDir, "settings.local.json"), 0o600); err != nil {
			return "", fmt.Errorf("launch.Materialize: copy settings.local.json: %w", err)
		}
	}

	if !isolated {
		// Create/repair symlinks for shared content.
		for _, name := range sharedSymlinks {
			src := filepath.Join(claudeConfigHome, name)
			dst := filepath.Join(isolateDir, name)
			if err := ensureSymlink(src, dst); err != nil {
				return "", fmt.Errorf("launch.Materialize: symlink %s: %w", name, err)
			}
		}
	}

	return isolateDir, nil
}

// copyCredentials copies credentials.json from profileDir to isolateDir.
func copyCredentials(profileDir, isolateDir string) error {
	src := filepath.Join(profileDir, "credentials.json")
	dst := filepath.Join(isolateDir, ".credentials.json")
	return copyFileToDir(src, dst, 0o600)
}

// copyFileToDir copies src to dst with the given mode.
func copyFileToDir(src, dst string, mode os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return fsio.WriteFileAtomic(dst, data, mode)
}

// ensureSymlink creates a symlink dst → src, repairing broken symlinks.
// No-op if dst already points to src correctly. Skips if src does not exist.
func ensureSymlink(src, dst string) error {
	// If src doesn't exist, skip.
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return nil
	}

	// Check if dst already exists and is a valid symlink to src.
	if link, err := os.Readlink(dst); err == nil {
		if link == src {
			// Symlink is correct — but verify it isn't broken.
			if _, statErr := os.Stat(dst); statErr == nil {
				return nil
			}
		}
		// Broken or wrong target — remove and recreate.
		if removeErr := os.Remove(dst); removeErr != nil {
			return fmt.Errorf("remove stale symlink %s: %w", dst, removeErr)
		}
	} else if !os.IsNotExist(err) {
		// dst exists but isn't a symlink — don't clobber it.
		return nil
	}

	return os.Symlink(src, dst)
}
