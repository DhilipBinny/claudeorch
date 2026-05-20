package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/DhilipBinny/claudeorch/internal/migrate"
	"github.com/DhilipBinny/claudeorch/internal/ui"
	"github.com/spf13/cobra"
)

func init() {
	registerSubcommand(func(root *cobra.Command) {
		root.AddCommand(newSessionCmd())
	})
}

func newSessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Manage Claude Code sessions (list, migrate, backups).",
		Long: `Discover, migrate, and manage Claude Code sessions stored in ~/.claude/projects/.

Session migration moves a session from one project directory to another
so that '/resume' in the new directory picks it up. All modifications
are backed up before execution.`,
	}
	cmd.AddCommand(newSessionListCmd())
	cmd.AddCommand(newSessionMigrateCmd())
	cmd.AddCommand(newSessionBackupsCmd())
	cmd.AddCommand(newSessionRestoreCmd())
	return cmd
}

func newSessionListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "list <directory>",
		Short:         "List all Claude Code sessions for a directory.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSessionList(cmd, args[0])
		},
	}
	return cmd
}

func runSessionList(cmd *cobra.Command, dir string) error {
	ui.Init(NoColor())

	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	projectDir, err := migrate.ProjectDir(absDir)
	if err != nil {
		return err
	}

	if _, err := os.Stat(projectDir); os.IsNotExist(err) {
		fmt.Fprintf(cmd.OutOrStdout(), "No Claude project found for: %s\n", absDir)
		fmt.Fprintf(cmd.OutOrStdout(), "  (looked for: %s)\n", projectDir)
		return nil
	}

	sessions, err := migrate.DiscoverSessions(projectDir)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()

	if len(sessions) == 0 {
		fmt.Fprintf(out, "No sessions found in: %s\n", absDir)
		return nil
	}

	fmt.Fprintf(out, "\nSessions for: %s\n", absDir)
	fmt.Fprintf(out, "Project dir:  %s\n", projectDir)
	fmt.Fprintf(out, "%s\n", dashLine(90))
	fmt.Fprintf(out, "%-4s %-40s %-22s %s\n", "#", "Session ID", "Modified", "Summary/Prompt")
	fmt.Fprintf(out, "%s\n", dashLine(90))

	for i, s := range sessions {
		modified := s.Modified
		if len(modified) > 19 {
			modified = modified[:19]
		}
		label := s.Summary
		if label == "" {
			label = s.FirstPrompt
		}
		if len(label) > 60 {
			label = label[:57] + "..."
		}
		sizeStr := ""
		if s.JSONLSize > 0 {
			sizeMB := float64(s.JSONLSize) / (1024 * 1024)
			sizeStr = fmt.Sprintf(" (%.1fMB)", sizeMB)
		}
		fmt.Fprintf(out, "%-4d %-40s %-22s %s%s\n", i+1, s.SessionID, modified, label, sizeStr)
	}

	fmt.Fprintf(out, "%s\n", dashLine(90))
	fmt.Fprintf(out, "Total: %d session(s)\n\n", len(sessions))
	return nil
}

func newSessionMigrateCmd() *cobra.Command {
	var (
		sourceDir string
		targetDir string
		sessionID string
		dryRun    bool
	)
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Migrate a session from one project directory to another.",
		Long: `Migrate a Claude Code session so that '/resume' works in the new directory.

Creates a backup before any changes. The JSONL conversation log is moved
with all 'cwd' fields rewritten to the new directory. Session indexes and
history.jsonl are updated accordingly.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSessionMigrate(cmd, sourceDir, targetDir, sessionID, dryRun)
		},
	}
	cmd.Flags().StringVar(&sourceDir, "from", "", "source project directory (required)")
	cmd.Flags().StringVar(&targetDir, "to", "", "target project directory (required)")
	cmd.Flags().StringVar(&sessionID, "session", "", "session ID to migrate (default: most recent)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be done without doing it")
	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("to")
	return cmd
}

func runSessionMigrate(cmd *cobra.Command, sourceDir, targetDir, sessionID string, dryRun bool) error {
	ui.Init(NoColor())

	absSource, err := filepath.Abs(sourceDir)
	if err != nil {
		return fmt.Errorf("resolve source path: %w", err)
	}
	absTarget, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("resolve target path: %w", err)
	}

	if absSource == absTarget {
		return fmt.Errorf("source and target directories are the same: %s", absSource)
	}

	opts := migrate.MigrateOptions{
		SourceDir: absSource,
		TargetDir: absTarget,
		SessionID: sessionID,
		DryRun:    dryRun,
	}

	out := cmd.OutOrStdout()

	if dryRun {
		actions, session, err := migrate.Plan(opts)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "\n[DRY RUN] Session: %s\n", session.SessionID)
		label := session.Summary
		if label == "" {
			label = session.FirstPrompt
		}
		if len(label) > 80 {
			label = label[:77] + "..."
		}
		fmt.Fprintf(out, "  Summary: %s\n", label)
		fmt.Fprintf(out, "  From:    %s\n", absSource)
		fmt.Fprintf(out, "  To:      %s\n\n", absTarget)
		fmt.Fprintf(out, "Would perform %d actions:\n", len(actions))
		for _, a := range actions {
			fmt.Fprintf(out, "  %s\n", a.Desc)
		}
		fmt.Fprintf(out, "\nRe-run without --dry-run to execute.\n\n")
		return nil
	}

	result, err := migrate.Execute(opts)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "\nMigrating session: %s\n", result.SessionID)
	fmt.Fprintf(out, "  From: %s\n", absSource)
	fmt.Fprintf(out, "  To:   %s\n\n", absTarget)
	fmt.Fprintf(out, "Backup: %s\n\n", result.BackupDir)

	for _, a := range result.Actions {
		fmt.Fprintf(out, "  %s\n", a)
	}

	fmt.Fprintf(out, "\nMigration complete!\n")
	fmt.Fprintf(out, "Run 'claude' in %s and use /resume to continue this session.\n\n", absTarget)
	return nil
}

func newSessionBackupsCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "backups",
		Short:         "List migration backups.",
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
	fmt.Fprintf(out, "%s\n", dashLine(60))
	for _, b := range backups {
		ts := "?"
		if !b.Timestamp.IsZero() {
			ts = b.Timestamp.Format("2006-01-02 15:04:05")
		}
		fmt.Fprintf(out, "  %s\n", b.Name)
		fmt.Fprintf(out, "    Date: %s  |  Files: %d\n", ts, b.FileCount)
	}
	fmt.Fprintln(out)
	return nil
}

func newSessionRestoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "restore <backup-dir>",
		Short:         "Show restore instructions for a backup.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSessionRestore(cmd, args[0])
		},
	}
}

func runSessionRestore(cmd *cobra.Command, backupDir string) error {
	absDir, err := filepath.Abs(backupDir)
	if err != nil {
		return fmt.Errorf("resolve backup path: %w", err)
	}

	if _, err := os.Stat(absDir); os.IsNotExist(err) {
		return fmt.Errorf("backup directory not found: %s", absDir)
	}

	files, err := migrate.RestoreInfo(absDir)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Backup contents:\n")
	for _, f := range files {
		fmt.Fprintf(out, "  %s\n", f)
	}
	fmt.Fprintf(out, "\nTo restore manually, copy .bak files back to their original locations.\n")
	fmt.Fprintf(out, "The backup filenames indicate where they came from.\n")
	return nil
}

func dashLine(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = '-'
	}
	return string(b)
}
