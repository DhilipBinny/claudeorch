//go:build faultinject

package fsio

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWriteFileAtomic_FaultInject_MidRename injects a failure at the rename
// step and verifies: the target file is absent, no temp file lingers, and the
// error is non-nil.
func TestWriteFileAtomic_FaultInject_MidRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.json")

	SetRenameFailAfter(1)
	t.Cleanup(ResetFaults)

	err := WriteFileAtomic(path, []byte("data"), 0o600)
	if err == nil {
		t.Fatal("expected error from injected fault")
	}

	// Target must not exist.
	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("target file should not exist after mid-rename fault")
	}

	// No temp files must linger.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		t.Errorf("temp file not cleaned up: %s", e.Name())
	}
}

// TestWriteFileAtomic_FaultInject_SecondCallSucceeds verifies that after
// SetRenameFailAfter(2) the first write succeeds and the second fails.
func TestWriteFileAtomic_FaultInject_SecondCallSucceeds(t *testing.T) {
	dir := t.TempDir()
	path1 := filepath.Join(dir, "first.json")
	path2 := filepath.Join(dir, "second.json")

	SetRenameFailAfter(2)
	t.Cleanup(ResetFaults)

	// First write should succeed.
	if err := WriteFileAtomic(path1, []byte("a"), 0o600); err != nil {
		t.Fatalf("first write should succeed: %v", err)
	}

	// Second write should fail.
	if err := WriteFileAtomic(path2, []byte("b"), 0o600); err == nil {
		t.Fatal("expected second write to fail")
	}

	// path2 must not exist.
	if _, statErr := os.Stat(path2); statErr == nil {
		t.Error("path2 should not exist after injected fault")
	}
}
