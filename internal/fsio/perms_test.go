package fsio

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureDir_CreatesWithMode(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "a", "b", "c")

	if err := EnsureDir(target, 0o700); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Error("target is not a directory")
	}
}

func TestEnsureDir_IdempotentOnExisting(t *testing.T) {
	base := t.TempDir()
	if err := EnsureDir(base, 0o700); err != nil {
		t.Fatalf("EnsureDir on existing dir: %v", err)
	}
}

func TestEnsureFile_CreatesIfMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "creds.json")

	if err := EnsureFile(path, 0o600); err != nil {
		t.Fatalf("EnsureFile: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() {
		t.Error("not a regular file")
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %04o, want 0600", info.Mode().Perm())
	}
}

func TestEnsureFile_IdempotentOnExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "creds.json")

	// Write some content first.
	if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}

	// EnsureFile must not truncate or error.
	if err := EnsureFile(path, 0o600); err != nil {
		t.Fatalf("EnsureFile on existing file: %v", err)
	}

	got, _ := os.ReadFile(path)
	if string(got) != "data" {
		t.Errorf("file content changed: %q", got)
	}
}

func TestEnsureFile_RejectsNonRegular(t *testing.T) {
	dir := t.TempDir()
	// Use the dir itself as the path — it's not a regular file.
	err := EnsureFile(dir, 0o600)
	// Should either be nil (os.IsExist → stat says not regular → error)
	// or an error — but never silently succeed.
	// Because dir exists, os.OpenFile returns IsExist → we check IsRegular.
	if err == nil {
		t.Error("expected error when path is a directory")
	}
}

func TestCheckPerms_Match(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckPerms(path, 0o600); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCheckPerms_Mismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckPerms(path, 0o600); err == nil {
		t.Error("expected error for permission mismatch")
	}
}

func TestCheckPerms_MissingFile(t *testing.T) {
	err := CheckPerms("/tmp/does-not-exist-xyz.json", 0o600)
	if err == nil {
		t.Error("expected error for missing file")
	}
}
