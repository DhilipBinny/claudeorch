package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setupBackupsCLI isolates both homes and seeds count backups, one hour
// apart, newest first. It returns the backup dirs in that order.
func setupBackupsCLI(t *testing.T, count int) []string {
	t.Helper()
	configHome := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configHome)
	t.Setenv("CLAUDEORCH_HOME", t.TempDir())
	backupsDir := filepath.Join(configHome, "backups")
	now := time.Now()
	dirs := make([]string, count)
	for i := range dirs {
		dirs[i] = filepath.Join(backupsDir, fmt.Sprintf("migration-s%03d-%d", i, now.Add(-time.Duration(i+1)*time.Hour).Unix()))
		if err := os.MkdirAll(dirs[i], 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dirs[i], "history.jsonl.bak"), []byte("h"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dirs
}

func runRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func countExisting(dirs []string) int {
	n := 0
	for _, d := range dirs {
		if _, err := os.Stat(d); err == nil {
			n++
		}
	}
	return n
}

func TestBackupsPrune_DryRunDeletesNothing(t *testing.T) {
	dirs := setupBackupsCLI(t, 3)

	out, err := runRoot(t, "session", "backups", "prune", "--keep", "1", "--dry-run")

	if err != nil {
		t.Fatalf("prune --dry-run: %v", err)
	}
	if !strings.Contains(out, "2 backup(s)") || !strings.Contains(out, "[DRY RUN]") {
		t.Errorf("output missing dry-run summary:\n%s", out)
	}
	if got := countExisting(dirs); got != 3 {
		t.Errorf("%d backups remain, want 3", got)
	}
}

func TestBackupsPrune_RefusesWithoutConfirmationWhenNotInteractive(t *testing.T) {
	if stdinIsTerminal() {
		t.Skip("stdin is a terminal; refusal path needs non-interactive stdin")
	}
	dirs := setupBackupsCLI(t, 2)

	_, err := runRoot(t, "session", "backups", "prune", "--keep", "0")

	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("err = %v, want refusal mentioning --yes", err)
	}
	if got := countExisting(dirs); got != 2 {
		t.Errorf("%d backups remain, want 2", got)
	}
}

func TestBackupsPrune_YesDeletesBeyondKeep(t *testing.T) {
	dirs := setupBackupsCLI(t, 3)

	out, err := runRoot(t, "session", "backups", "prune", "--keep", "1", "--yes")

	if err != nil {
		t.Fatalf("prune --yes: %v", err)
	}
	if !strings.Contains(out, "Pruned 2 backup(s)") {
		t.Errorf("output = %q, want Pruned 2", out)
	}
	if _, err := os.Stat(dirs[0]); err != nil {
		t.Errorf("newest backup must be kept: %v", err)
	}
	if got := countExisting(dirs); got != 1 {
		t.Errorf("%d backups remain, want 1", got)
	}
}

func TestBackupsPrune_RejectsOverflowingOlderThanDays(t *testing.T) {
	dirs := setupBackupsCLI(t, 2)

	_, err := runRoot(t, "session", "backups", "prune", "--keep", "0", "--older-than-days", "281474976710656", "--yes")

	if err == nil || !strings.Contains(err.Error(), "--older-than-days") {
		t.Fatalf("err = %v, want --older-than-days range error", err)
	}
	if got := countExisting(dirs); got != 2 {
		t.Errorf("%d backups remain, want 2", got)
	}
}

func TestBackupsPrune_RejectsNegativeKeep(t *testing.T) {
	setupBackupsCLI(t, 1)

	_, err := runRoot(t, "session", "backups", "prune", "--keep", "-1", "--yes")

	if err == nil {
		t.Fatal("expected error for negative --keep")
	}
}

// TestSessionBackups_RejectsUnknownSubcommand guards against a typo such as
// "backups prnue" silently falling through to the list action.
func TestSessionBackups_RejectsUnknownSubcommand(t *testing.T) {
	setupBackupsCLI(t, 0)

	_, err := runRoot(t, "session", "backups", "prnue")

	if err == nil || !strings.Contains(err.Error(), "prnue") {
		t.Fatalf("err = %v, want unknown-command error naming %q", err, "prnue")
	}
}

func TestBackupsPrune_FinishesLeftoverWhenNothingToPrune(t *testing.T) {
	dirs := setupBackupsCLI(t, 1)
	leftover := filepath.Join(filepath.Dir(dirs[0]), ".pruning-migration-x-1")
	if err := os.MkdirAll(leftover, 0o700); err != nil {
		t.Fatal(err)
	}

	out, err := runRoot(t, "session", "backups", "prune", "--yes")

	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if !strings.Contains(out, "No migration backups to prune") {
		t.Errorf("output = %q", out)
	}
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Errorf("leftover should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(dirs[0]); err != nil {
		t.Errorf("valid backup must be kept: %v", err)
	}
}
