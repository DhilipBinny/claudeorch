package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateBackupDir(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configHome)

	sid := "abcdef12-3456-7890-abcd-ef1234567890"
	dir, err := CreateBackupDir(sid)
	if err != nil {
		t.Fatalf("CreateBackupDir: %v", err)
	}

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Fatal("backup dir not created")
	}

	base := filepath.Base(dir)
	if !strings.HasPrefix(base, "migration-abcdef12-") {
		t.Errorf("backup dir name = %q, want prefix 'migration-abcdef12-'", base)
	}
}

func TestBackupFile(t *testing.T) {
	dir := t.TempDir()
	backupDir := filepath.Join(dir, "backup")
	os.MkdirAll(backupDir, 0o755)

	srcPath := filepath.Join(dir, "original.txt")
	os.WriteFile(srcPath, []byte("important data"), 0o644)

	if err := BackupFile(backupDir, srcPath, "original.txt.bak"); err != nil {
		t.Fatalf("BackupFile: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(backupDir, "original.txt.bak"))
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(data) != "important data" {
		t.Errorf("backup content = %q", data)
	}
}

func TestBackupFile_MissingSource(t *testing.T) {
	backupDir := t.TempDir()

	err := BackupFile(backupDir, "/nonexistent/file.txt", "backup.bak")
	if err != nil {
		t.Fatalf("should silently skip missing source, got: %v", err)
	}

	if _, err := os.Stat(filepath.Join(backupDir, "backup.bak")); !os.IsNotExist(err) {
		t.Error("backup file should not exist for missing source")
	}
}

func TestListBackups_Empty(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configHome)

	backups, err := ListBackups()
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(backups) != 0 {
		t.Errorf("backups = %d, want 0", len(backups))
	}
}

func TestListBackups_Multiple(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configHome)

	backupsDir := filepath.Join(configHome, "backups")
	os.MkdirAll(filepath.Join(backupsDir, "migration-abc-1000000"), 0o755)
	os.MkdirAll(filepath.Join(backupsDir, "migration-def-2000000"), 0o755)
	os.MkdirAll(filepath.Join(backupsDir, "not-a-migration"), 0o755)

	// Add a file to one of them
	os.WriteFile(filepath.Join(backupsDir, "migration-abc-1000000", "test.bak"), []byte("data"), 0o644)

	backups, err := ListBackups()
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(backups) != 2 {
		t.Fatalf("backups = %d, want 2", len(backups))
	}

	// Should be sorted newest first
	if !strings.Contains(backups[0].Name, "2000000") {
		t.Errorf("first backup = %q, want newer one", backups[0].Name)
	}

	// Check file count
	found := false
	for _, b := range backups {
		if strings.Contains(b.Name, "abc") {
			if b.FileCount != 1 {
				t.Errorf("FileCount = %d, want 1", b.FileCount)
			}
			found = true
		}
	}
	if !found {
		t.Error("abc backup not found")
	}
}

func TestRestoreInfo(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "session.jsonl.bak"), []byte("data"), 0o644)
	os.WriteFile(filepath.Join(dir, "sessions-index.json.bak"), []byte("index"), 0o644)

	files, err := RestoreInfo(dir)
	if err != nil {
		t.Fatalf("RestoreInfo: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %d, want 2", len(files))
	}
}

func TestRestoreInfo_NonexistentDir(t *testing.T) {
	_, err := RestoreInfo("/nonexistent/dir")
	if err == nil {
		t.Fatal("expected error for nonexistent dir")
	}
}
