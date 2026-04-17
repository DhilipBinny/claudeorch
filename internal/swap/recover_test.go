package swap

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestRecover_RemovesOrphanTmpDirForDeadPID(t *testing.T) {
	orchHome := t.TempDir()
	// PID 2^31 - 1 is almost certainly dead; use it as a dead sentinel.
	deadDir := filepath.Join(orchHome, "tmp-swap-2147483647")
	if err := os.MkdirAll(deadDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deadDir, "staged.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	Recover(orchHome, t.TempDir())

	if _, err := os.Stat(deadDir); !os.IsNotExist(err) {
		t.Errorf("expected dead-PID tmp dir to be removed, but it still exists")
	}
}

func TestRecover_KeepsTmpDirForLivePID(t *testing.T) {
	orchHome := t.TempDir()
	// Our own PID is alive by definition.
	liveDir := filepath.Join(orchHome, fmt.Sprintf("tmp-swap-%d", os.Getpid()))
	if err := os.MkdirAll(liveDir, 0o700); err != nil {
		t.Fatal(err)
	}

	Recover(orchHome, t.TempDir())

	if _, err := os.Stat(liveDir); err != nil {
		t.Errorf("live-PID tmp dir should be preserved, got: %v", err)
	}
}

func TestRecover_IgnoresNonTmpDirs(t *testing.T) {
	orchHome := t.TempDir()
	unrelated := filepath.Join(orchHome, "profiles")
	if err := os.MkdirAll(unrelated, 0o700); err != nil {
		t.Fatal(err)
	}
	malformed := filepath.Join(orchHome, "tmp-swap-notapid")
	if err := os.MkdirAll(malformed, 0o700); err != nil {
		t.Fatal(err)
	}

	Recover(orchHome, t.TempDir())

	if _, err := os.Stat(unrelated); err != nil {
		t.Errorf("unrelated dir removed: %v", err)
	}
	// Malformed names (non-integer PID) must be ignored, not removed.
	if _, err := os.Stat(malformed); err != nil {
		t.Errorf("dir with malformed PID suffix wrongly removed: %v", err)
	}
}

func TestRecover_MissingClaudeorchHome_DoesNotPanic(t *testing.T) {
	// Must be a no-op when the dir doesn't exist.
	Recover(filepath.Join(t.TempDir(), "does-not-exist"), t.TempDir())
}

func TestRecover_ReportsPreSwapBackupButKeepsIt(t *testing.T) {
	orchHome := t.TempDir()
	configHome := t.TempDir()
	orphan := filepath.Join(configHome, ".credentials.json.pre-swap")
	if err := os.WriteFile(orphan, []byte("backup"), 0o600); err != nil {
		t.Fatal(err)
	}

	Recover(orchHome, configHome)

	// Report-only: the file must still exist (not destroyed).
	if _, err := os.Stat(orphan); err != nil {
		t.Errorf("pre-swap backup should be preserved for manual inspection: %v", err)
	}
}
