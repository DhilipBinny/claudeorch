// Package swap implements the two-file atomic account swap:
//
//	credentials.json + .claude.json are moved together under a single flock.
//
// On Linux, both files are on disk and swapped via the classic five-stage
// rename dance (stage → backup → commitA → commitB → cleanup) with
// rollback on commitB failure.
//
// On macOS, credentials live in the system Keychain (not on disk), so
// commitA becomes a Keychain write instead of a file rename. Only
// .claude.json is still file-renamed (commitB). Rollback restores the
// Keychain entry from a pre-write backup.
//
// Orphaned tmp-swap-<pid>/ or *.pre-swap files left by crashes are cleaned
// by Recover() which runs in root PreRun.
package swap

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/DhilipBinny/claudeorch/internal/creds"
	"github.com/DhilipBinny/claudeorch/internal/fsio"
	"github.com/DhilipBinny/claudeorch/internal/schema"
)

// Run performs a full swap of the live Claude credentials to the given profile.
//
// It does NOT acquire the global lock — callers must hold it before calling Run.
// It DOES create claudeorchHome/tmp-swap-<pid>/ as the staging directory.
//
// profileDir     is <claudeorchHome>/profiles/<name>/
// claudeorchHome is ~/.claudeorch (for staging dir placement)
// claudeConfigHome is the resolved ~/.claude dir
// claudeJSONPath is the resolved ~/.claude.json (or inside configHome if CLAUDE_CONFIG_DIR set)
func Run(profileDir, claudeorchHome, claudeConfigHome, claudeJSONPath string) error {
	apiKey, err := profileAPIKey(profileDir)
	if err != nil {
		return err
	}
	if creds.IsKeychainBased() {
		if apiKey != "" {
			return ErrAPIKeySwapUnsupported
		}
		return runKeychainSwap(profileDir, claudeConfigHome, claudeJSONPath)
	}
	if apiKey != "" {
		return runFileAPIKeySwap(profileDir, claudeConfigHome, claudeJSONPath, apiKey)
	}
	return runFileSwap(profileDir, claudeorchHome, claudeConfigHome, claudeJSONPath)
}

// ErrAPIKeySwapUnsupported is returned when asked to swap the live account to
// an API-key profile while credentials live in the macOS Keychain. Making
// Claude Code use an API key there means deleting its OAuth Keychain item,
// which also holds MCP tokens, and has not been verified against a real
// Mac. 'launch' still works for such profiles.
var ErrAPIKeySwapUnsupported = errors.New(
	"swapping to an API-key profile is not supported on macOS yet " +
		"(it would have to delete Claude Code's OAuth Keychain item); " +
		"use 'claudeorch launch <name>' to run it instead")

// profileAPIKey returns the API key stored in the profile, or "" when the
// profile holds an OAuth login.
func profileAPIKey(profileDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(profileDir, "credentials.json"))
	if err != nil {
		return "", fmt.Errorf("swap: read profile credentials: %w", err)
	}
	parsed, err := schema.ParseCredentials(data)
	if err != nil {
		return "", fmt.Errorf("swap: parse profile credentials: %w", err)
	}
	if parsed.Type == schema.CredentialAPIKey {
		return parsed.APIKey, nil
	}
	return "", nil
}

// runFileAPIKeySwap makes the live account an API-key profile on Linux.
//
// Claude Code reads an API key only from ANTHROPIC_API_KEY, apiKeyHelper, or
// primaryApiKey in .claude.json; it ignores an "apiKey" in
// .credentials.json. So the key goes into .claude.json (taken from the
// profile, with primaryApiKey set), and the OAuth login is removed from
// .credentials.json so Claude Code does not prefer it. Every other top-level
// key in .credentials.json, notably mcpOAuth, is preserved.
func runFileAPIKeySwap(profileDir, claudeConfigHome, claudeJSONPath, apiKey string) error {
	slog.Debug("swap: starting (file mode, API key)", "profile_dir", profileDir)

	srcClaude, err := os.ReadFile(filepath.Join(profileDir, "claude.json"))
	if err != nil {
		return fmt.Errorf("swap: read profile claude.json: %w", err)
	}
	newClaude, err := setTopLevelString(srcClaude, "primaryApiKey", apiKey)
	if err != nil {
		return fmt.Errorf("swap: prepare .claude.json: %w", err)
	}

	liveCreds := filepath.Join(claudeConfigHome, ".credentials.json")
	var newCreds []byte // nil means "no credentials file needed"
	if liveData, readErr := os.ReadFile(liveCreds); readErr == nil {
		newCreds, err = dropTopLevelKey(liveData, "claudeAiOauth")
		if err != nil {
			return fmt.Errorf("swap: live %s is not valid JSON, refusing to rewrite it: %w", liveCreds, err)
		}
	} else if !os.IsNotExist(readErr) {
		return fmt.Errorf("swap: read live credentials: %w", readErr)
	}

	backupCreds := liveCreds + ".pre-swap"
	backupClaude := claudeJSONPath + ".pre-swap"
	if _, err := os.Stat(liveCreds); err == nil {
		if err := os.Rename(liveCreds, backupCreds); err != nil {
			return fmt.Errorf("swap: backup credentials: %w", err)
		}
	}
	if _, err := os.Stat(claudeJSONPath); err == nil {
		if err := os.Rename(claudeJSONPath, backupClaude); err != nil {
			if _, statErr := os.Stat(backupCreds); statErr == nil {
				_ = os.Rename(backupCreds, liveCreds)
			}
			return fmt.Errorf("swap: backup .claude.json: %w", err)
		}
	}

	fail := func(what string, cause error) error {
		// Undo exactly what ran: drop anything we wrote, then restore backups.
		_ = os.Remove(liveCreds)
		_ = os.Remove(claudeJSONPath)
		if rbErr := rollback(backupCreds, liveCreds, backupClaude, claudeJSONPath); rbErr != nil {
			return fmt.Errorf("swap: %s: %v; rollback also failed: %w", what, cause, rbErr)
		}
		return fmt.Errorf("swap: %s: %w", what, cause)
	}

	if newCreds != nil {
		if err := fsio.WriteFileAtomic(liveCreds, newCreds, 0o600); err != nil {
			return fail("write credentials", err)
		}
	}
	if err := fsio.WriteFileAtomic(claudeJSONPath, newClaude, 0o600); err != nil {
		return fail("write .claude.json", err)
	}

	_ = os.Remove(backupCreds)
	_ = os.Remove(backupClaude)
	slog.Debug("swap: complete (file mode, API key)")
	return nil
}

// setTopLevelString returns data (a JSON object) with key set to value and
// every other top-level key kept as it was.
func setTopLevelString(data []byte, key, value string) ([]byte, error) {
	m := map[string]json.RawMessage{}
	if len(strings.TrimSpace(string(data))) > 0 {
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, err
		}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	m[key] = raw
	return json.Marshal(m)
}

// dropTopLevelKey returns data (a JSON object) without key, keeping every
// other top-level key. It returns nil when nothing is left.
func dropTopLevelKey(data []byte, key string) ([]byte, error) {
	m := map[string]json.RawMessage{}
	if len(strings.TrimSpace(string(data))) > 0 {
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, err
		}
	}
	delete(m, key)
	if len(m) == 0 {
		return nil, nil
	}
	return json.Marshal(m)
}

// runKeychainSwap handles the macOS path where credentials live in the
// Keychain and .claude.json is on disk. Only one file rename is needed.
func runKeychainSwap(profileDir, claudeConfigHome, claudeJSONPath string) error {
	slog.Debug("swap: starting (keychain mode)", "profile_dir", profileDir)

	// Read the incoming profile's credentials.
	srcCreds := filepath.Join(profileDir, "credentials.json")
	newCredsData, err := os.ReadFile(srcCreds)
	if err != nil {
		return fmt.Errorf("swap: read profile credentials: %w", err)
	}

	srcClaude := filepath.Join(profileDir, "claude.json")
	liveCreds := filepath.Join(claudeConfigHome, ".credentials.json")

	// Backup current Keychain state so we can rollback if .claude.json
	// rename fails.
	slog.Debug("swap: backing up current keychain credentials")
	backupData, _ := creds.ReadLive(liveCreds) // nil if empty/missing — OK

	// CommitA: write new credentials to Keychain.
	slog.Debug("swap: writing new credentials to keychain")
	if err := creds.WriteLive(liveCreds, newCredsData); err != nil {
		return fmt.Errorf("swap: write credentials to keychain: %w", err)
	}

	// CommitB: rename .claude.json.
	backupClaude := claudeJSONPath + ".pre-swap"
	if _, err := os.Stat(claudeJSONPath); err == nil {
		if err := os.Rename(claudeJSONPath, backupClaude); err != nil {
			// Rollback Keychain.
			if backupData != nil {
				_ = creds.WriteLive(liveCreds, backupData)
			}
			return fmt.Errorf("swap: backup .claude.json: %w", err)
		}
	}
	slog.Debug("swap: renaming .claude.json")
	if err := copyFile(srcClaude, claudeJSONPath, 0o600); err != nil {
		// Rollback: restore .claude.json backup + Keychain.
		if _, statErr := os.Stat(backupClaude); statErr == nil {
			_ = os.Rename(backupClaude, claudeJSONPath)
		}
		if backupData != nil {
			_ = creds.WriteLive(liveCreds, backupData)
		}
		return fmt.Errorf("swap: write .claude.json: %w", err)
	}

	// Cleanup.
	_ = os.Remove(backupClaude)
	slog.Debug("swap: complete (keychain mode)")
	return nil
}

// runFileSwap handles the Linux path where both credentials and identity
// are flat files. Uses the classic five-stage rename dance.
func runFileSwap(profileDir, claudeorchHome, claudeConfigHome, claudeJSONPath string) error {
	pid := os.Getpid()
	tmpDir := filepath.Join(claudeorchHome, fmt.Sprintf("tmp-swap-%d", pid))
	slog.Debug("swap: starting (file mode)", "profile_dir", profileDir, "tmp_dir", tmpDir)

	// Stage 1: Copy profile files to tmp staging dir.
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return fmt.Errorf("swap: create staging dir: %w", err)
	}

	srcCreds := filepath.Join(profileDir, "credentials.json")
	srcClaude := filepath.Join(profileDir, "claude.json")
	dstCredsStage := filepath.Join(tmpDir, "credentials.json")
	dstClaudeStage := filepath.Join(tmpDir, "claude.json")

	if err := copyFile(srcCreds, dstCredsStage, 0o600); err != nil {
		_ = os.RemoveAll(tmpDir)
		return fmt.Errorf("swap: stage credentials: %w", err)
	}
	if err := copyFile(srcClaude, dstClaudeStage, 0o600); err != nil {
		_ = os.RemoveAll(tmpDir)
		return fmt.Errorf("swap: stage claude.json: %w", err)
	}

	slog.Debug("swap: stage 1 complete (files staged)")

	// Stage 2: Backup live files.
	liveCreds := filepath.Join(claudeConfigHome, ".credentials.json")
	backupCreds := liveCreds + ".pre-swap"
	backupClaude := claudeJSONPath + ".pre-swap"

	// Only backup if they exist.
	if _, err := os.Stat(liveCreds); err == nil {
		if err := os.Rename(liveCreds, backupCreds); err != nil {
			_ = os.RemoveAll(tmpDir)
			return fmt.Errorf("swap: backup credentials: %w", err)
		}
	}
	if _, err := os.Stat(claudeJSONPath); err == nil {
		if err := os.Rename(claudeJSONPath, backupClaude); err != nil {
			// Restore credentials backup.
			if _, statErr := os.Stat(backupCreds); statErr == nil {
				_ = os.Rename(backupCreds, liveCreds)
			}
			_ = os.RemoveAll(tmpDir)
			return fmt.Errorf("swap: backup .claude.json: %w", err)
		}
	}

	slog.Debug("swap: stage 2 complete (backups created)")

	// Stage 3: CommitA — move staged credentials into place.
	if err := os.Rename(dstCredsStage, liveCreds); err != nil {
		if rollbackErr := rollback(backupCreds, liveCreds, backupClaude, claudeJSONPath); rollbackErr != nil {
			_ = os.RemoveAll(tmpDir)
			return fmt.Errorf("swap: commitA failed (%v); rollback also failed: %w", err, rollbackErr)
		}
		_ = os.RemoveAll(tmpDir)
		return fmt.Errorf("swap: commitA credentials: %w", err)
	}

	slog.Debug("swap: stage 3 complete (credentials committed)")

	// Stage 4: CommitB — move staged .claude.json into place.
	if err := os.Rename(dstClaudeStage, claudeJSONPath); err != nil {
		if rollbackErr := rollback(backupCreds, liveCreds, backupClaude, claudeJSONPath); rollbackErr != nil {
			_ = os.RemoveAll(tmpDir)
			return fmt.Errorf("swap: commitB failed (%v); rollback also failed: %w", err, rollbackErr)
		}
		_ = os.RemoveAll(tmpDir)
		return fmt.Errorf("swap: commitB .claude.json: %w", err)
	}

	slog.Debug("swap: stage 4 complete (.claude.json committed)")

	// Stage 5: Cleanup backups + staging dir.
	_ = os.Remove(backupCreds)
	_ = os.Remove(backupClaude)
	_ = os.RemoveAll(tmpDir)

	slog.Debug("swap: complete")
	return nil
}

// rollback restores the .pre-swap backups to their original locations.
// Errors in rollback are returned wrapped but do not affect the caller's original error.
func rollback(backupCreds, liveCreds, backupClaude, claudeJSONPath string) error {
	var errs []error

	// Restore credentials.
	if _, err := os.Stat(backupCreds); err == nil {
		if err := os.Rename(backupCreds, liveCreds); err != nil {
			errs = append(errs, fmt.Errorf("restore credentials: %w", err))
		}
	}
	// Restore .claude.json.
	if _, err := os.Stat(backupClaude); err == nil {
		if err := os.Rename(backupClaude, claudeJSONPath); err != nil {
			errs = append(errs, fmt.Errorf("restore .claude.json: %w", err))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// copyFile copies src to dst at the given mode, syncing before returning.
func copyFile(src, dst string, mode os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
