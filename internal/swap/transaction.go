// Package swap implements the two-file atomic account swap:
//
//	credentials.json + .claude.json are moved together under a single flock.
//
// Swap stages (all file ops under global lock):
//  1. Stage  — write incoming profile files into tmp-swap-<pid>/
//  2. Backup — rename current live files to *.pre-swap (backup)
//  3. CommitA — rename tmp-swap-<pid>/credentials.json → ~/.claude/.credentials.json
//  4. CommitB — rename tmp-swap-<pid>/claude.json → <claudeJSONPath>
//  5. Cleanup — remove *.pre-swap backups + empty tmp dir
//
// If CommitB fails, rollback restores from *.pre-swap.
// Orphaned tmp-swap-<pid>/ or *.pre-swap files left by crashes are cleaned
// by Recover() which runs in root PreRun.
package swap

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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
	pid := os.Getpid()
	tmpDir := filepath.Join(claudeorchHome, fmt.Sprintf("tmp-swap-%d", pid))
	slog.Debug("swap: starting", "profile_dir", profileDir, "tmp_dir", tmpDir)

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
