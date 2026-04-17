package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/DhilipBinny/claudeorch/internal/fsio"
	"github.com/DhilipBinny/claudeorch/internal/launch"
	"github.com/DhilipBinny/claudeorch/internal/paths"
	"github.com/DhilipBinny/claudeorch/internal/profile"
	"github.com/spf13/cobra"
)

func init() {
	registerSubcommand(func(root *cobra.Command) {
		root.AddCommand(newLaunchCmd())
	})
}

func newLaunchCmd() *cobra.Command {
	var isolated bool
	cmd := &cobra.Command{
		Use:   "launch <name> [-- claude-args...]",
		Short: "Launch Claude Code with a specific profile in isolate mode.",
		Long: `Materializes an isolate directory for the named profile and execs
'claude' with CLAUDE_CONFIG_DIR pointing at it.

By default, shared content (CLAUDE.md, projects/, skills/, settings.json) is
symlinked from the default Claude config home. Use --isolated to skip all
symlinks for a fully independent session.

Pass extra arguments to claude after --:
  claudeorch launch work -- --dangerously-skip-permissions`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLaunch(cmd, isolated, args)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
		// Disable flag parsing after first non-flag arg so -- pass-through works.
		TraverseChildren: false,
	}
	cmd.Flags().BoolVar(&isolated, "isolated", false, "no symlinks — fully independent session")
	cmd.Flags().SetInterspersed(false) // stop parsing flags after first non-flag
	return cmd
}

func runLaunch(cmd *cobra.Command, isolated bool, args []string) error {
	name := args[0]
	var claudeArgs []string
	if len(args) > 1 {
		claudeArgs = args[1:]
	}

	if err := paths.ValidateProfileName(name); err != nil {
		return err
	}

	storePath, err := paths.StoreFile()
	if err != nil {
		return err
	}
	store, err := profile.Load(storePath)
	if err != nil {
		return fmt.Errorf("load store: %w", err)
	}
	if _, ok := store.Profiles[name]; !ok {
		return fmt.Errorf("profile %q not found", name)
	}

	profileDir, err := paths.ProfileDir(name)
	if err != nil {
		return err
	}
	isolateDir, err := paths.IsolateDir(name)
	if err != nil {
		return err
	}
	claudeConfigHome, err := paths.ClaudeConfigHome()
	if err != nil {
		return err
	}

	// Ensure parent of isolate dir exists.
	if err := fsio.EnsureDir(filepath.Dir(isolateDir), 0o700); err != nil {
		return err
	}

	// Materialize the isolate directory (idempotent).
	if _, err := launch.Materialize(profileDir, isolateDir, claudeConfigHome, isolated); err != nil {
		return fmt.Errorf("materialize isolate: %w", err)
	}

	// Release lock before exec (lock is not needed post-materialize).
	lockPath, err := paths.LockFile()
	if err != nil {
		return err
	}
	if err := fsio.EnsureDir(filepath.Dir(lockPath), 0o700); err != nil {
		return err
	}

	// Acquire lock briefly to update LastUsedAt, then release before exec.
	// LastUsedAt is best-effort metadata — a save failure is warned but not fatal
	// because the profile's credentials are already materialized and launch must proceed.
	if release, lockErr := fsio.AcquireLock(context.Background(), lockPath); lockErr == nil {
		store.Profiles[name].LastUsedAt = timeNow()
		if saveErr := store.Save(storePath); saveErr != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: could not update LastUsedAt in store: %v\n", saveErr)
		}
		_ = release()
	} else {
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: could not acquire lock to update LastUsedAt: %v\n", lockErr)
	}

	fmt.Fprintf(cmd.ErrOrStderr(), "Launching claude with profile %q (CLAUDE_CONFIG_DIR=%s)\n",
		name, isolateDir)

	// Exec replaces this process — defers above have already run.
	return launch.Exec("", isolateDir, claudeArgs)
}
