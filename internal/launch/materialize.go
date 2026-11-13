// Package launch handles creation of per-profile isolate directories and
// exec-ing into Claude Code with the right CLAUDE_CONFIG_DIR.
package launch

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/DhilipBinny/claudeorch/internal/fsio"
	"github.com/DhilipBinny/claudeorch/internal/schema"
	"github.com/DhilipBinny/claudeorch/internal/session"
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
// It is safe to call while another claude process is already running in the
// same isolate (a second launch of the same profile, e.g. from another
// folder). Credentials and .claude.json are only copied in from the profile
// when the isolate has none, or when the profile's tokens are strictly
// fresher. A running session rotates tokens in the isolate and keeps project
// state in .claude.json; blindly overwriting either would log it out or
// discard its state.
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

	// Copy credentials only when the profile is the fresher source, and never
	// into an isolate a running session owns without a credentials file: on
	// macOS Claude Code deletes .credentials.json once the login is in the
	// Keychain, so a missing file next to an existing .claude.json looks
	// like "nothing here" but is really "the session holds the login".
	refreshed := false
	if !runningSessionOwns(isolateDir) {
		var err error
		refreshed, err = syncCredentials(profileDir, isolateDir)
		if err != nil {
			return "", err
		}
	}

	// .claude.json is live session state once claude is running. Seed it
	// when absent, and re-seed it when we just replaced the credentials
	// (the identity may have changed with them). Otherwise leave it alone.
	claudeJSON := filepath.Join(isolateDir, ".claude.json")
	if _, statErr := os.Stat(claudeJSON); refreshed || os.IsNotExist(statErr) {
		if err := copyFileToDir(filepath.Join(profileDir, "claude.json"),
			claudeJSON, 0o600); err != nil {
			return "", fmt.Errorf("launch.Materialize: copy .claude.json: %w", err)
		}
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

// runningSessionOwns reports whether a live claude session is using
// isolateDir and has already set it up (it has a .claude.json but no
// credentials file). Such an isolate must be left exactly as it is.
func runningSessionOwns(isolateDir string) bool {
	if _, err := os.Stat(filepath.Join(isolateDir, ".claude.json")); err != nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(isolateDir, ".credentials.json")); err == nil {
		return false
	}
	live, err := session.HasLiveSession(isolateDir)
	return err == nil && live
}

// syncCredentials copies credentials.json from profileDir into the isolate
// as .credentials.json, but only when the isolate has none or the profile's
// copy is fresher (see profileIsFresher). It reports whether it wrote.
// A missing profile credentials file is not an error and writes nothing.
func syncCredentials(profileDir, isolateDir string) (bool, error) {
	src := filepath.Join(profileDir, "credentials.json")
	dst := filepath.Join(isolateDir, ".credentials.json")

	srcData, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("launch.Materialize: read profile credentials: %w", err)
	}

	if dstData, err := os.ReadFile(dst); err == nil && !profileIsFresher(srcData, dstData) {
		return false, nil
	}

	if err := fsio.WriteFileAtomic(dst, srcData, 0o600); err != nil {
		return false, fmt.Errorf("launch.Materialize: write credentials: %w", err)
	}
	return true, nil
}

// profileIsFresher reports whether the profile's credential blob should
// replace the isolate's.
//
//   - Unusable isolate copy: replace it.
//   - Unusable profile copy: keep the isolate's, never clobber good with bad.
//   - Otherwise replace only if the profile's expiresAt is strictly later.
//     Ties keep the isolate, which is what a running session owns.
func profileIsFresher(profileData, isolateData []byte) bool {
	iso, isoErr := schema.ParseCredentials(isolateData)
	if isoErr != nil {
		return true
	}
	prof, profErr := schema.ParseCredentials(profileData)
	if profErr != nil {
		return false
	}
	return prof.ExpiresAt.After(iso.ExpiresAt)
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
