package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/DhilipBinny/claudeorch/internal/migrate"
	"github.com/spf13/cobra"
)

const (
	backupTimeLayout = "2006-01-02 15:04:05"
	pruneConfirmWord = "prune"
	hoursPerDay      = 24
	// maxOlderThanDays keeps --older-than-days far below the point where
	// converting it to a time.Duration would overflow.
	maxOlderThanDays     = 100 * 365
	backupListDashLength = 60
)

func newSessionBackupsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backups",
		Short: "List or prune migration backups.",
		Long: `Migration backups live in ~/.claude/backups/migration-<session>-<unix-ts>/.
Each holds pre-migration copies of the session log, history.jsonl, session
indexes and MEMORY.md files.

Running 'backups' with no subcommand lists them (same as 'backups list').

After every successful migration, backups older than 30 days are pruned
automatically, always keeping the 10 newest. Use 'backups prune' to clean
up on demand.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSessionBackups(cmd)
		},
	}
	cmd.AddCommand(newSessionBackupsListCmd())
	cmd.AddCommand(newSessionBackupsPruneCmd())
	return cmd
}

func newSessionBackupsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "list",
		Short:         "List migration backups, newest first.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSessionBackups(cmd)
		},
	}
}

func runSessionBackups(cmd *cobra.Command) error {
	backups, err := migrate.ListBackups()
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if len(backups) == 0 {
		fmt.Fprintln(out, "No migration backups found.")
		return nil
	}

	fmt.Fprintf(out, "\nMigration backups:\n")
	printBackupTable(out, backups)
	return nil
}

func printBackupTable(out io.Writer, backups []migrate.BackupInfo) {
	fmt.Fprintf(out, "%s\n", dashLine(backupListDashLength))
	var total int64
	for _, b := range backups {
		ts := "?"
		if !b.Timestamp.IsZero() {
			ts = b.Timestamp.Format(backupTimeLayout)
		}
		fmt.Fprintf(out, "  %s\n", b.Name)
		fmt.Fprintf(out, "    Date: %s  |  Files: %d  |  Size: %s\n", ts, b.FileCount, humanBytes(b.Size))
		total += b.Size
	}
	fmt.Fprintf(out, "%s\n", dashLine(backupListDashLength))
	fmt.Fprintf(out, "  %d backup(s), %s total\n\n", len(backups), humanBytes(total))
}

func newSessionBackupsPruneCmd() *cobra.Command {
	var (
		keep          int
		olderThanDays int
		dryRun        bool
		yes           bool
	)
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Delete old migration backups (zero-overwrites files first).",
		Long: `Delete migration backups beyond the newest --keep, optionally only those
older than --older-than-days. Backups whose name has no parseable timestamp
are never pruned. Only migration-* directories are touched; other files in
~/.claude/backups/ (such as Claude Code's own .claude.json backups) are left
alone.

Files are zero-overwritten before deletion. This is best-effort: on
copy-on-write filesystems (APFS, Btrfs, ZFS), SSDs, and volumes with
snapshots (e.g. Time Machine local snapshots) the original data may persist.
Full-disk encryption (FileVault, LUKS) is the reliable protection for data
at rest.

Deleted backups cannot be restored. Interactive confirmation is required
unless --yes is given.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if olderThanDays < 0 || olderThanDays > maxOlderThanDays {
				return fmt.Errorf("--older-than-days must be between 0 and %d, got %d", maxOlderThanDays, olderThanDays)
			}
			opts := migrate.PruneOptions{
				Keep:      keep,
				OlderThan: time.Duration(olderThanDays) * hoursPerDay * time.Hour,
			}
			return runSessionBackupsPrune(cmd, opts, dryRun, yes)
		},
	}
	cmd.Flags().IntVar(&keep, "keep", migrate.DefaultBackupKeep, "number of newest backups to always keep")
	cmd.Flags().IntVar(&olderThanDays, "older-than-days", 0, "only prune backups older than this many days (0 = no age limit)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be deleted without deleting")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip confirmation")
	return cmd
}

func runSessionBackupsPrune(cmd *cobra.Command, opts migrate.PruneOptions, dryRun, yes bool) error {
	out := cmd.OutOrStdout()
	errOut := cmd.ErrOrStderr()

	candidates, err := migrate.PlanPrune(opts)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		fmt.Fprintln(out, "No migration backups to prune.")
		if dryRun {
			return nil
		}
		// An empty confirmed set deletes no backups but still finishes any
		// removal left incomplete by an earlier run.
		if _, err := migrate.PruneBackups(opts, []migrate.BackupInfo{}); err != nil {
			return fmt.Errorf("prune finished with problems: %w", err)
		}
		return nil
	}

	fmt.Fprintf(out, "\nBackups to delete:\n")
	printBackupTable(out, candidates)

	if dryRun {
		fmt.Fprintln(out, "[DRY RUN] Nothing deleted. Re-run without --dry-run to prune.")
		return nil
	}

	if !yes {
		confirmed, err := confirmPrune(errOut)
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Fprintln(out, "Prune cancelled.")
			return nil
		}
	}

	// Restrict deletion to the previewed set: if a migration ran since the
	// preview, nothing the user did not see is removed.
	removed, err := migrate.PruneBackups(opts, candidates)
	var freed int64
	for _, b := range removed {
		freed += b.Size
	}
	fmt.Fprintf(out, "Pruned %d backup(s), freed %s.\n", len(removed), humanBytes(freed))
	if err != nil {
		return fmt.Errorf("prune finished with problems: %w", err)
	}
	return nil
}

func confirmPrune(errOut io.Writer) (bool, error) {
	if !stdinIsTerminal() {
		return false, fmt.Errorf("prune requires interactive confirmation (or --yes for non-interactive)")
	}
	fmt.Fprintf(errOut, "Type '%s' to permanently delete these backups: ", pruneConfirmWord)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false, fmt.Errorf("read confirmation: %w", err)
	}
	return strings.TrimSpace(line) == pruneConfirmWord, nil
}

// printPlannedAutoPrune lists the backups a migration would delete through
// automatic retention, so a migrate --dry-run shows every deletion.
func printPlannedAutoPrune(out io.Writer) {
	planned, err := migrate.PlanAutoPrune()
	if err != nil {
		fmt.Fprintf(out, "\nCould not determine backup retention: %v\n", err)
		return
	}
	if len(planned) == 0 {
		return
	}
	fmt.Fprintf(out, "\nBackup retention would delete %d old migration backup(s):\n", len(planned))
	for _, b := range planned {
		fmt.Fprintf(out, "  %s (%s)\n", b.Name, b.Timestamp.Format(backupTimeLayout))
	}
}
