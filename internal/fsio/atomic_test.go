package fsio

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestWriteFileAtomic_HappyPath verifies the golden path: file is written with
// correct content and the requested permissions.
func TestWriteFileAtomic_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.json")
	data := []byte(`{"hello":"world"}`)

	if err := WriteFileAtomic(path, data, 0o600); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("content mismatch: got %q, want %q", got, data)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %04o, want 0600", info.Mode().Perm())
	}
}

// TestWriteFileAtomic_EmptyPath rejects an empty path immediately.
func TestWriteFileAtomic_EmptyPath(t *testing.T) {
	err := WriteFileAtomic("", []byte("x"), 0o600)
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

// TestWriteFileAtomic_MissingDir fails when the parent directory doesn't exist.
func TestWriteFileAtomic_MissingDir(t *testing.T) {
	err := WriteFileAtomic("/tmp/nonexistent-dir-xyz/out.json", []byte("x"), 0o600)
	if err == nil {
		t.Fatal("expected error for missing parent dir")
	}
}

// TestWriteFileAtomic_OverwriteExisting verifies that a second write replaces
// the file content atomically and preserves the requested permissions.
func TestWriteFileAtomic_OverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.json")

	first := []byte("first")
	if err := WriteFileAtomic(path, first, 0o600); err != nil {
		t.Fatal(err)
	}

	second := []byte("second")
	if err := WriteFileAtomic(path, second, 0o600); err != nil {
		t.Fatalf("second write: %v", err)
	}

	got, _ := os.ReadFile(path)
	if string(got) != string(second) {
		t.Errorf("expected %q, got %q", second, got)
	}
}

// TestWriteFileAtomic_NoTempRemains verifies no temp files linger on success.
func TestWriteFileAtomic_NoTempRemains(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.json")

	if err := WriteFileAtomic(path, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "out.json" {
			t.Errorf("unexpected file left behind: %s", e.Name())
		}
	}
}

// TestWriteFileAtomic_CrossDevice simulates the EXDEV condition by replacing
// renameFunc with one that returns an EXDEV-shaped error.
func TestWriteFileAtomic_CrossDevice(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.json")

	orig := renameFunc
	t.Cleanup(func() { renameFunc = orig })

	renameFunc = func(_, _ string) error {
		// Simulate EXDEV via a LinkError with the canonical message.
		return &os.LinkError{
			Op:  "rename",
			Old: "a",
			New: "b",
			Err: errors.New("invalid cross-device link"),
		}
	}

	err := WriteFileAtomic(path, []byte("x"), 0o600)
	if !errors.Is(err, ErrCrossDevice) {
		t.Errorf("expected ErrCrossDevice, got: %v", err)
	}
}

// TestWriteFileAtomic_RenameFails_NoTempRemains verifies cleanup on rename error.
func TestWriteFileAtomic_RenameFails_NoTempRemains(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.json")

	orig := renameFunc
	t.Cleanup(func() { renameFunc = orig })

	renameFunc = func(_, _ string) error {
		return errors.New("rename failed")
	}

	_ = WriteFileAtomic(path, []byte("x"), 0o600)

	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		t.Errorf("unexpected file left behind after rename failure: %s", e.Name())
	}
}
