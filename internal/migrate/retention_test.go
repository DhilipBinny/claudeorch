package migrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAutoPruneBackups_HonorsMinAgeAndKeep(t *testing.T) {
	backupsDir := setupPruneEnv(t)
	now := time.Now()
	var recent, old []string
	for i := 0; i < DefaultBackupKeep+2; i++ {
		recent = append(recent, seedBackup(t, backupsDir, fmt.Sprintf("r%03d", i), now.Add(-time.Duration(i+1)*time.Hour)))
	}
	for i := 0; i < 3; i++ {
		old = append(old, seedBackup(t, backupsDir, fmt.Sprintf("o%03d", i), now.Add(-AutoPruneMinAge-time.Duration(i+1)*time.Hour)))
	}

	actions := autoPruneBackups()

	for _, d := range recent {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("recent backup %s must be kept: %v", filepath.Base(d), err)
		}
	}
	for _, d := range old {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Errorf("old backup %s should be pruned", filepath.Base(d))
		}
	}
	if len(actions) != 1 || !strings.HasPrefix(actions[0], "[pruned] 3 ") {
		t.Errorf("actions = %v, want one [pruned] 3 entry", actions)
	}
}

func TestAutoPruneBackups_NoActionWhenNothingPruned(t *testing.T) {
	backupsDir := setupPruneEnv(t)
	seedBackup(t, backupsDir, "aaaa", time.Now())

	if actions := autoPruneBackups(); len(actions) != 0 {
		t.Errorf("actions = %v, want none", actions)
	}
}

func TestExecute_AppliesBackupRetention(t *testing.T) {
	configHome, sourceDir, targetDir := setupTestEnv(t)
	t.Setenv("CLAUDEORCH_HOME", t.TempDir())
	backupsDir := filepath.Join(configHome, "backups")
	sourceSlug, _ := PathToSlug(sourceDir)
	sourceProject := filepath.Join(configHome, "projects", sourceSlug)
	sid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	seedIndex(t, sourceProject, IndexEntry{SessionID: sid, ProjectPath: sourceDir})
	seedJSONL(t, sourceProject, sid, `{"type":"user","cwd":"`+sourceDir+`"}`)
	if err := os.MkdirAll(backupsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	var recent []string
	for i := 0; i < DefaultBackupKeep; i++ {
		recent = append(recent, seedBackup(t, backupsDir, fmt.Sprintf("r%03d", i), time.Now().Add(-time.Duration(i+1)*time.Hour)))
	}
	stale := seedBackup(t, backupsDir, "stale", time.Now().Add(-AutoPruneMinAge-24*time.Hour))

	result, err := Execute(MigrateOptions{SourceDir: sourceDir, TargetDir: targetDir})

	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if _, err := os.Stat(result.BackupDir); err != nil {
		t.Errorf("backup for this migration must exist: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale backup should be pruned after migration")
	}
	for _, d := range recent {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("recent backup %s must be kept: %v", filepath.Base(d), err)
		}
	}
	if !strings.Contains(strings.Join(result.Actions, "\n"), "[pruned] 1 ") {
		t.Errorf("actions = %v, want a [pruned] 1 entry", result.Actions)
	}
}

func TestRemoveBackup_FailedRemovalNeverLeavesValidLookingBackup(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	backupsDir := setupPruneEnv(t)
	dir := seedBackup(t, backupsDir, "aaaa", time.Now().Add(-time.Hour))
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	tomb := filepath.Join(backupsDir, tombstonePrefix+filepath.Base(dir))
	t.Cleanup(func() { os.Chmod(tomb, 0o700) })

	err := RemoveBackup(dir)

	if err == nil {
		t.Fatal("expected error when backup dir cannot be emptied")
	}
	backups, listErr := ListBackups()
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(backups) != 0 {
		t.Errorf("partially destroyed backup still listed as valid: %v", names(backups))
	}
	if _, statErr := os.Stat(tomb); statErr != nil {
		t.Errorf("tombstone should remain for retry: %v", statErr)
	}
}

func TestPruneBackups_FinishesInterruptedRemoval(t *testing.T) {
	backupsDir := setupPruneEnv(t)
	leftover := filepath.Join(backupsDir, tombstonePrefix+"migration-aaaa-1")
	if err := os.MkdirAll(leftover, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leftover, "x.bak"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := PruneBackups(PruneOptions{Keep: DefaultBackupKeep}, nil); err != nil {
		t.Fatalf("PruneBackups: %v", err)
	}

	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Errorf("leftover tombstone should be removed, stat err = %v", err)
	}
}

func TestPruneBackups_ReportsIncompleteOverwrite(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can open read-only files for writing")
	}
	backupsDir := setupPruneEnv(t)
	dir := seedBackup(t, backupsDir, "aaaa", time.Now().Add(-time.Hour))
	if err := os.Chmod(filepath.Join(dir, "history.jsonl.bak"), 0o400); err != nil {
		t.Fatal(err)
	}

	removed, err := PruneBackups(PruneOptions{Keep: 0}, nil)

	if !errors.Is(err, ErrOverwriteIncomplete) {
		t.Errorf("err = %v, want ErrOverwriteIncomplete", err)
	}
	if len(removed) != 1 {
		t.Errorf("removed %d, want 1 (deletion proceeds despite overwrite failure)", len(removed))
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Errorf("backup should be deleted, stat err = %v", statErr)
	}
}

func TestPlanAutoPrune_AccountsForUpcomingBackup(t *testing.T) {
	backupsDir := setupPruneEnv(t)
	now := time.Now()
	for i := 0; i < DefaultBackupKeep; i++ {
		seedBackup(t, backupsDir, fmt.Sprintf("o%03d", i), now.Add(-AutoPruneMinAge-time.Duration(i+1)*time.Hour))
	}

	planned, err := PlanAutoPrune()

	if err != nil {
		t.Fatalf("PlanAutoPrune: %v", err)
	}
	if len(planned) != 1 || !strings.Contains(planned[0].Name, fmt.Sprintf("o%03d", DefaultBackupKeep-1)) {
		t.Errorf("planned = %v, want only the oldest backup", names(planned))
	}
}
