package migrate

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DhilipBinny/claudeorch/internal/fsio"
	"github.com/DhilipBinny/claudeorch/internal/paths"
)

const (
	// DefaultBackupKeep is how many of the newest migration backups are
	// always retained, both by automatic retention and by `backups prune`.
	DefaultBackupKeep = 10

	// AutoPruneMinAge is the minimum age a backup must reach before automatic
	// retention after a migration may delete it. A burst of migrations in a
	// short window therefore keeps every rollback point until it ages out.
	AutoPruneMinAge = 30 * 24 * time.Hour

	backupDirPrefix = "migration-"

	// tombstonePrefix marks a backup that is being destroyed. A backup is
	// renamed to this prefix before any byte is overwritten, so an
	// interrupted or failed removal can never leave a zeroed directory that
	// still looks like a valid, restorable backup.
	tombstonePrefix = ".pruning-"

	// zeroChunkSize bounds memory used while overwriting backup files, which
	// can be hundreds of megabytes for long sessions.
	zeroChunkSize = 64 * 1024
)

// ErrOverwriteIncomplete reports that a backup was deleted but some of its
// files could not be zero-overwritten first, so their plaintext may remain
// recoverable from the underlying storage.
var ErrOverwriteIncomplete = errors.New("zero-overwrite incomplete")

// PruneOptions selects which migration backups to delete.
type PruneOptions struct {
	// Keep is the number of newest backups that are never deleted.
	Keep int
	// OlderThan, when > 0, restricts deletion to backups older than this.
	OlderThan time.Duration
}

// Validate rejects negative retention values.
func (o PruneOptions) Validate() error {
	if o.Keep < 0 {
		return fmt.Errorf("migrate: keep must be >= 0, got %d", o.Keep)
	}
	if o.OlderThan < 0 {
		return fmt.Errorf("migrate: older-than must be >= 0, got %s", o.OlderThan)
	}
	return nil
}

// SelectPrunable returns the backups eligible for deletion under opts.
// backups must be sorted newest first (as returned by ListBackups).
// Backups whose timestamp could not be parsed are never selected, since
// their age — and so their place in the retention order — is unknown.
func SelectPrunable(backups []BackupInfo, opts PruneOptions, now time.Time) []BackupInfo {
	var cutoff time.Time
	if opts.OlderThan > 0 {
		cutoff = now.Add(-opts.OlderThan)
	}

	var out []BackupInfo
	kept := 0
	for _, b := range backups {
		if b.Timestamp.IsZero() {
			continue
		}
		if kept < opts.Keep {
			kept++
			continue
		}
		if !cutoff.IsZero() && !b.Timestamp.Before(cutoff) {
			continue
		}
		out = append(out, b)
	}
	return out
}

// autoPruneOptions is the retention policy applied after every migration.
func autoPruneOptions() PruneOptions {
	return PruneOptions{Keep: DefaultBackupKeep, OlderThan: AutoPruneMinAge}
}

// PlanAutoPrune returns the backups that automatic retention would delete
// after the next migration. That migration adds one newer backup, so one
// fewer existing backup falls inside the keep window.
func PlanAutoPrune() ([]BackupInfo, error) {
	opts := autoPruneOptions()
	if opts.Keep > 0 {
		opts.Keep--
	}
	return PlanPrune(opts)
}

// PlanPrune returns the backups that PruneBackups would delete under opts.
func PlanPrune(opts PruneOptions) ([]BackupInfo, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	backups, err := ListBackups()
	if err != nil {
		return nil, err
	}
	return SelectPrunable(backups, opts, time.Now()), nil
}

// PruneBackups deletes migration backups selected by opts under the
// claudeorch lock, so it cannot race a concurrent migration.
//
// When confirmed is non-nil, only backups that are both selected under the
// lock and present in confirmed (matched by name) are deleted. This lets a
// caller show a PlanPrune preview, ask the user, and then guarantee nothing
// outside that preview is removed even if the backup set changed meanwhile.
func PruneBackups(opts PruneOptions, confirmed []BackupInfo) ([]BackupInfo, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}

	var allowed map[string]bool
	if confirmed != nil {
		allowed = make(map[string]bool, len(confirmed))
		for _, b := range confirmed {
			allowed[b.Name] = true
		}
	}

	release, err := acquireLock()
	if err != nil {
		return nil, err
	}
	defer release()

	return pruneBackupsLocked(opts, allowed)
}

// acquireLock takes the claudeorch-wide lock, creating its parent dir if
// needed so session commands work before any profile has been added.
func acquireLock() (release func() error, err error) {
	lockPath, err := paths.LockFile()
	if err != nil {
		return nil, fmt.Errorf("migrate: resolve lock file: %w", err)
	}
	if err := fsio.EnsureDir(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, fmt.Errorf("migrate: create lock dir: %w", err)
	}
	release, err = fsio.AcquireLock(context.Background(), lockPath)
	if err != nil {
		return nil, fmt.Errorf("migrate: acquire lock: %w", err)
	}
	return release, nil
}

// pruneBackupsLocked performs the deletion. The caller must hold the lock.
// A nil allowed map means every selected backup may be deleted. Leftovers
// from earlier interrupted removals are cleaned up first. It keeps going
// after a per-backup failure and returns what was removed alongside a
// joined error describing every problem.
func pruneBackupsLocked(opts PruneOptions, allowed map[string]bool) ([]BackupInfo, error) {
	errs := []error{cleanupTombstones()}

	backups, err := ListBackups()
	if err != nil {
		return nil, errors.Join(append(errs, err)...)
	}

	var removed []BackupInfo
	for _, b := range SelectPrunable(backups, opts, time.Now()) {
		if allowed != nil && !allowed[b.Name] {
			continue
		}
		err := RemoveBackup(b.Dir)
		if err == nil || errors.Is(err, ErrOverwriteIncomplete) {
			removed = append(removed, b)
		}
		errs = append(errs, err)
	}
	return removed, errors.Join(errs...)
}

// RemoveBackup destroys a migration backup directory: it is first renamed
// to a tombstone name, then every regular file is zero-overwritten, then the
// tree is deleted. The directory must sit directly inside the Claude backups
// dir and carry the migration- prefix; anything else is refused so a bad
// path can never delete outside the backup set.
//
// Zero-overwrite is best-effort: on copy-on-write or journaling filesystems
// and SSDs the original blocks may survive. If some files could not be
// overwritten the backup is still deleted, since leaving the plaintext in
// place is strictly worse, and the returned error wraps
// ErrOverwriteIncomplete.
func RemoveBackup(dir string) error {
	backupsDir, err := paths.ClaudeBackupsDir()
	if err != nil {
		return err
	}
	if err := checkBackupContainment(backupsDir, dir); err != nil {
		return err
	}

	tomb := filepath.Join(filepath.Clean(backupsDir), tombstonePrefix+filepath.Base(filepath.Clean(dir)))
	if err := os.Rename(dir, tomb); err != nil {
		return fmt.Errorf("migrate: remove backup %s: %w", dir, err)
	}
	return destroyTree(tomb)
}

// destroyTree zero-overwrites and deletes a tombstoned backup directory.
func destroyTree(tomb string) error {
	overwriteErr := zeroOverwriteTree(tomb)
	if err := os.RemoveAll(tomb); err != nil {
		return fmt.Errorf("migrate: backup contents destroyed but %s could not be removed (will retry on next prune): %w", tomb, err)
	}
	if overwriteErr != nil {
		return fmt.Errorf("migrate: %s deleted, but %w: %v", tomb, ErrOverwriteIncomplete, overwriteErr)
	}
	return nil
}

// cleanupTombstones finishes removals that were interrupted or failed on an
// earlier run.
func cleanupTombstones() error {
	backupsDir, err := paths.ClaudeBackupsDir()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(backupsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("migrate: read backups dir: %w", err)
	}

	var errs []error
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), tombstonePrefix+backupDirPrefix) {
			continue
		}
		if err := destroyTree(filepath.Join(backupsDir, e.Name())); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func checkBackupContainment(backupsDir, dir string) error {
	cleanRoot := filepath.Clean(backupsDir)
	cleanDir := filepath.Clean(dir)
	if filepath.Dir(cleanDir) != cleanRoot || !strings.HasPrefix(filepath.Base(cleanDir), backupDirPrefix) {
		return fmt.Errorf("migrate: refusing to remove %q: not a migration backup under %s", dir, backupsDir)
	}
	info, err := os.Lstat(cleanDir)
	if err != nil {
		return fmt.Errorf("migrate: stat backup %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("migrate: refusing to remove %q: not a directory", dir)
	}
	return nil
}

// zeroOverwriteTree overwrites every regular file under root with zeros.
// Symlinks are not followed and hard-linked files are skipped, so only data
// that exists solely inside root is touched.
func zeroOverwriteTree(root string) error {
	var errs []error
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			errs = append(errs, err)
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if err := zeroOverwriteFile(path); err != nil {
			errs = append(errs, err)
		}
		return nil
	})
	if walkErr != nil {
		errs = append(errs, walkErr)
	}
	return errors.Join(errs...)
}

func zeroOverwriteFile(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|openNoFollow, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if hasOtherLinks(info) {
		// Unlinking is still safe; overwriting would corrupt the other name.
		return f.Close()
	}

	zeros := make([]byte, zeroChunkSize)
	for remaining := info.Size(); remaining > 0; {
		n := int64(len(zeros))
		if remaining < n {
			n = remaining
		}
		if _, err := f.Write(zeros[:n]); err != nil {
			f.Close()
			return fmt.Errorf("overwrite %s: %w", path, err)
		}
		remaining -= n
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync %s: %w", path, err)
	}
	return f.Close()
}
