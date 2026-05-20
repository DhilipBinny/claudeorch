package migrate

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/DhilipBinny/claudeorch/internal/paths"
)

// BackupInfo describes a migration backup directory.
type BackupInfo struct {
	Dir       string
	Name      string
	Timestamp time.Time
	FileCount int
}

// CreateBackupDir creates a timestamped backup directory for a migration.
// Returns the full path to the created directory.
func CreateBackupDir(sessionID string) (string, error) {
	backupsDir, err := paths.ClaudeBackupsDir()
	if err != nil {
		return "", err
	}
	prefix := sessionID
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	name := fmt.Sprintf("migration-%s-%d", prefix, time.Now().Unix())
	dir := filepath.Join(backupsDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("migrate: create backup dir: %w", err)
	}
	return dir, nil
}

// BackupFile copies a file into the backup directory with the given name.
// Silently skips if the source doesn't exist.
func BackupFile(backupDir, srcPath, backupName string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("migrate: backup open %s: %w", srcPath, err)
	}
	defer src.Close()

	dstPath := filepath.Join(backupDir, backupName)
	dst, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("migrate: backup create %s: %w", dstPath, err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("migrate: backup copy: %w", err)
	}
	return dst.Close()
}

// ListBackups returns all migration backup directories, sorted newest first.
func ListBackups() ([]BackupInfo, error) {
	backupsDir, err := paths.ClaudeBackupsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(backupsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("migrate: read backups dir: %w", err)
	}

	var backups []BackupInfo
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "migration-") {
			continue
		}
		bi := BackupInfo{
			Dir:  filepath.Join(backupsDir, e.Name()),
			Name: e.Name(),
		}
		parts := strings.Split(e.Name(), "-")
		if len(parts) >= 3 {
			if ts, err := strconv.ParseInt(parts[len(parts)-1], 10, 64); err == nil {
				bi.Timestamp = time.Unix(ts, 0)
			}
		}
		subEntries, _ := os.ReadDir(bi.Dir)
		bi.FileCount = len(subEntries)
		backups = append(backups, bi)
	}

	sort.Slice(backups, func(i, j int) bool {
		return backups[i].Timestamp.After(backups[j].Timestamp)
	})
	return backups, nil
}

// RestoreInfo lists the contents of a backup directory.
func RestoreInfo(backupDir string) ([]string, error) {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return nil, fmt.Errorf("migrate: read backup dir: %w", err)
	}
	var files []string
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fmt.Sprintf("%s (%d bytes)", e.Name(), info.Size()))
	}
	sort.Strings(files)
	return files, nil
}
