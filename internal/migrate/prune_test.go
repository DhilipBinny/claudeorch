package migrate

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// setupPruneEnv points both Claude and claudeorch homes at temp dirs so
// tests never touch the real ~/.claude or take the real claudeorch lock.

func setupPruneEnv(t *testing.T) (backupsDir string) {
	t.Helper()
	configHome := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configHome)
	t.Setenv("CLAUDEORCH_HOME", t.TempDir())
	backupsDir = filepath.Join(configHome, "backups")
	if err := os.MkdirAll(backupsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	return backupsDir
}

// seedBackup creates migration-<id>-<ts> with one file and returns its path.

func seedBackup(t *testing.T, backupsDir, id string, ts time.Time) string {
	t.Helper()
	dir := filepath.Join(backupsDir, fmt.Sprintf("migration-%s-%d", id, ts.Unix()))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "history.jsonl.bak"), []byte("secret-history"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func backupsAt(now time.Time, ages ...time.Duration) []BackupInfo {
	out := make([]BackupInfo, len(ages))
	for i, age := range ages {
		out[i] = BackupInfo{Name: fmt.Sprintf("b%d", i), Timestamp: now.Add(-age)}
	}
	return out
}

func names(bs []BackupInfo) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.Name
	}
	return out
}

func TestSelectPrunable_KeepsNewestN(t *testing.T) {
	now := time.Now()
	backups := backupsAt(now, 1*time.Hour, 2*time.Hour, 3*time.Hour, 4*time.Hour)

	got := SelectPrunable(backups, PruneOptions{Keep: 2}, now)

	if want := []string{"b2", "b3"}; !reflect.DeepEqual(names(got), want) {
		t.Errorf("selected = %v, want %v", names(got), want)
	}
}

func TestSelectPrunable_KeepZeroSelectsAll(t *testing.T) {
	now := time.Now()
	backups := backupsAt(now, time.Hour, 2*time.Hour)

	got := SelectPrunable(backups, PruneOptions{Keep: 0}, now)

	if len(got) != 2 {
		t.Errorf("selected %d, want 2", len(got))
	}
}

func TestSelectPrunable_KeepExceedsCount(t *testing.T) {
	now := time.Now()
	backups := backupsAt(now, time.Hour, 2*time.Hour)

	got := SelectPrunable(backups, PruneOptions{Keep: 10}, now)

	if len(got) != 0 {
		t.Errorf("selected %v, want none", names(got))
	}
}

func TestSelectPrunable_OlderThanRestrictsBeyondKeep(t *testing.T) {
	now := time.Now()
	day := 24 * time.Hour
	backups := backupsAt(now, 1*day, 5*day, 40*day, 50*day)

	got := SelectPrunable(backups, PruneOptions{Keep: 1, OlderThan: 30 * day}, now)

	if want := []string{"b2", "b3"}; !reflect.DeepEqual(names(got), want) {
		t.Errorf("selected = %v, want %v", names(got), want)
	}
}

func TestSelectPrunable_KeepProtectsOldBackups(t *testing.T) {
	now := time.Now()
	day := 24 * time.Hour
	backups := backupsAt(now, 40*day, 50*day, 60*day)

	got := SelectPrunable(backups, PruneOptions{Keep: 2, OlderThan: 30 * day}, now)

	if want := []string{"b2"}; !reflect.DeepEqual(names(got), want) {
		t.Errorf("selected = %v, want %v", names(got), want)
	}
}

func TestSelectPrunable_SkipsUnparseableTimestamp(t *testing.T) {
	now := time.Now()
	backups := append(backupsAt(now, time.Hour), BackupInfo{Name: "no-ts"})

	got := SelectPrunable(backups, PruneOptions{Keep: 0}, now)

	if want := []string{"b0"}; !reflect.DeepEqual(names(got), want) {
		t.Errorf("selected = %v, want %v", names(got), want)
	}
}

func TestPruneOptions_ValidateRejectsNegativeKeep(t *testing.T) {
	if err := (PruneOptions{Keep: -1}).Validate(); err == nil {
		t.Fatal("expected error for negative keep")
	}
}

func TestPruneOptions_ValidateRejectsNegativeOlderThan(t *testing.T) {
	if err := (PruneOptions{OlderThan: -time.Hour}).Validate(); err == nil {
		t.Fatal("expected error for negative older-than")
	}
}

func TestPlanPrune_DoesNotDelete(t *testing.T) {
	backupsDir := setupPruneEnv(t)
	now := time.Now()
	old := seedBackup(t, backupsDir, "old", now.Add(-2*time.Hour))
	seedBackup(t, backupsDir, "new", now.Add(-time.Hour))

	got, err := PlanPrune(PruneOptions{Keep: 1})

	if err != nil {
		t.Fatalf("PlanPrune: %v", err)
	}
	if len(got) != 1 || got[0].Dir != old {
		t.Fatalf("plan = %v, want [%s]", names(got), filepath.Base(old))
	}
	if _, err := os.Stat(old); err != nil {
		t.Errorf("PlanPrune removed %s: %v", old, err)
	}
}

func TestPruneBackups_RemovesSelectedAndKeepsRest(t *testing.T) {
	backupsDir := setupPruneEnv(t)
	now := time.Now()
	oldest := seedBackup(t, backupsDir, "aaaa", now.Add(-3*time.Hour))
	middle := seedBackup(t, backupsDir, "bbbb", now.Add(-2*time.Hour))
	newest := seedBackup(t, backupsDir, "cccc", now.Add(-time.Hour))

	removed, err := PruneBackups(PruneOptions{Keep: 1}, nil)

	if err != nil {
		t.Fatalf("PruneBackups: %v", err)
	}
	if len(removed) != 2 {
		t.Fatalf("removed %d, want 2", len(removed))
	}
	for _, gone := range []string{oldest, middle} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s should be removed, stat err = %v", gone, err)
		}
	}
	if _, err := os.Stat(newest); err != nil {
		t.Errorf("newest backup should be kept: %v", err)
	}
}

func TestPruneBackups_OnlyRemovesConfirmed(t *testing.T) {
	backupsDir := setupPruneEnv(t)
	now := time.Now()
	confirmedDir := seedBackup(t, backupsDir, "aaaa", now.Add(-3*time.Hour))
	unconfirmedDir := seedBackup(t, backupsDir, "bbbb", now.Add(-2*time.Hour))
	seedBackup(t, backupsDir, "cccc", now.Add(-time.Hour))
	confirmed := []BackupInfo{{Name: filepath.Base(confirmedDir)}}

	removed, err := PruneBackups(PruneOptions{Keep: 1}, confirmed)

	if err != nil {
		t.Fatalf("PruneBackups: %v", err)
	}
	if want := []string{filepath.Base(confirmedDir)}; !reflect.DeepEqual(names(removed), want) {
		t.Errorf("removed = %v, want %v", names(removed), want)
	}
	if _, err := os.Stat(unconfirmedDir); err != nil {
		t.Errorf("unconfirmed backup must survive: %v", err)
	}
}

func TestPruneBackups_IgnoresNonMigrationEntries(t *testing.T) {
	backupsDir := setupPruneEnv(t)
	claudeOwn := filepath.Join(backupsDir, ".claude.json.backup.1792739150443")
	if err := os.WriteFile(claudeOwn, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	otherDir := filepath.Join(backupsDir, "unrelated")
	if err := os.MkdirAll(otherDir, 0o700); err != nil {
		t.Fatal(err)
	}
	seedBackup(t, backupsDir, "aaaa", time.Now().Add(-time.Hour))

	if _, err := PruneBackups(PruneOptions{Keep: 0}, nil); err != nil {
		t.Fatalf("PruneBackups: %v", err)
	}

	for _, p := range []string{claudeOwn, otherDir} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s must not be touched: %v", p, err)
		}
	}
}

func TestPruneBackups_CreatesMissingLockDir(t *testing.T) {
	backupsDir := setupPruneEnv(t)
	seedBackup(t, backupsDir, "aaaa", time.Now().Add(-time.Hour))

	if _, err := PruneBackups(PruneOptions{Keep: 0}, nil); err != nil {
		t.Fatalf("PruneBackups without existing lock dir: %v", err)
	}
}

func TestRemoveBackup_RefusesPathOutsideBackupsDir(t *testing.T) {
	setupPruneEnv(t)
	outside := filepath.Join(t.TempDir(), "migration-evil-1")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}

	err := RemoveBackup(outside)

	if err == nil {
		t.Fatal("expected refusal for path outside backups dir")
	}
	if _, statErr := os.Stat(outside); statErr != nil {
		t.Errorf("refused path was modified: %v", statErr)
	}
}

func TestRemoveBackup_RefusesNonMigrationName(t *testing.T) {
	backupsDir := setupPruneEnv(t)
	dir := filepath.Join(backupsDir, "keepme")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := RemoveBackup(dir); err == nil {
		t.Fatal("expected refusal for non-migration dir name")
	}
}

func TestRemoveBackup_RefusesTraversal(t *testing.T) {
	backupsDir := setupPruneEnv(t)

	err := RemoveBackup(filepath.Join(backupsDir, "migration-x-1", "..", ".."))

	if err == nil {
		t.Fatal("expected refusal for traversal path")
	}
}

func TestRemoveBackup_RefusesSymlinkedBackup(t *testing.T) {
	backupsDir := setupPruneEnv(t)
	target := t.TempDir()
	victim := filepath.Join(target, "victim.txt")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(backupsDir, "migration-link-1")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	err := RemoveBackup(link)

	if err == nil {
		t.Fatal("expected refusal for symlinked backup dir")
	}
	if data, _ := os.ReadFile(victim); string(data) != "keep" {
		t.Errorf("symlink target was modified: %q", data)
	}
}

func TestRemoveBackup_DoesNotFollowInnerSymlink(t *testing.T) {
	backupsDir := setupPruneEnv(t)
	dir := seedBackup(t, backupsDir, "aaaa", time.Now())
	victim := filepath.Join(t.TempDir(), "victim.txt")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, "link.bak")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	if err := RemoveBackup(dir); err != nil {
		t.Fatalf("RemoveBackup: %v", err)
	}

	if data, _ := os.ReadFile(victim); string(data) != "keep" {
		t.Errorf("file behind inner symlink was overwritten: %q", data)
	}
}

func TestZeroOverwriteFile_ZeroesContentAcrossChunks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.bak")
	size := zeroChunkSize*2 + 123
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), size), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := zeroOverwriteFile(path); err != nil {
		t.Fatalf("zeroOverwriteFile: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != size {
		t.Fatalf("size changed: %d, want %d", len(data), size)
	}
	if !bytes.Equal(data, make([]byte, size)) {
		t.Error("file not fully zeroed")
	}
}

func TestListBackups_ReportsSize(t *testing.T) {
	backupsDir := setupPruneEnv(t)
	seedBackup(t, backupsDir, "aaaa", time.Now())

	backups, err := ListBackups()

	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(backups) != 1 || backups[0].Size != int64(len("secret-history")) {
		t.Errorf("backups = %+v, want one with size %d", backups, len("secret-history"))
	}
}

func TestListBackups_TieBreaksByName(t *testing.T) {
	backupsDir := setupPruneEnv(t)
	ts := time.Now()
	seedBackup(t, backupsDir, "aaaa", ts)
	seedBackup(t, backupsDir, "bbbb", ts)

	backups, err := ListBackups()

	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(backups) != 2 || !strings.Contains(backups[0].Name, "bbbb") {
		t.Errorf("order = %v, want bbbb first", names(backups))
	}
}

func TestZeroOverwriteFile_SkipsHardLinkedFile(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "history.jsonl")
	if err := os.WriteFile(live, []byte("live-history"), 0o600); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(dir, "history.jsonl.bak")
	if err := os.Link(live, backup); err != nil {
		t.Skipf("hard links unsupported: %v", err)
	}

	if err := zeroOverwriteFile(backup); err != nil {
		t.Fatalf("zeroOverwriteFile: %v", err)
	}

	if data, _ := os.ReadFile(live); string(data) != "live-history" {
		t.Errorf("hard-linked live file was overwritten: %q", data)
	}
}
