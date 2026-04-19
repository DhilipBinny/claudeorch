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
	// Preliminary load for fast fail-on-missing-name — avoids acquiring the
	// lock just to tell the user they mistyped a profile name.
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

	// Acquire the global lock. Reconcile + double-launch check + state
	// transitions all happen under lock. Materialize runs AFTER release
	// because it's per-profile (isolate/<name>/) and the lock protects
	// store.json, not per-profile dirs.
	lockPath, err := paths.LockFile()
	if err != nil {
		return err
	}
	if err := fsio.EnsureDir(filepath.Dir(lockPath), 0o700); err != nil {
		return err
	}
	release, err := fsio.AcquireLock(context.Background(), lockPath)
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	lockReleased := false
	defer func() {
		if !lockReleased {
			_ = release()
		}
	}()

	// Reload authoritative store under lock.
	store, err = profile.Load(storePath)
	if err != nil {
		return fmt.Errorf("load store: %w", err)
	}
	p, ok := store.Profiles[name]
	if !ok {
		return fmt.Errorf("profile %q not found", name)
	}

	// Freshest-wins: pull any newer tokens (from live if it matches, or
	// from a prior isolate session) back into profile/<name> before we
	// materialize. Without this, launch would overwrite fresh tokens in
	// isolate with stale tokens from profile — the v0.1.0 bug.
	if _, err := reconcileProfiles(store, cmd.ErrOrStderr()); err != nil {
		return fmt.Errorf("reconcile: %w", err)
	}

	// Refuse double-book: profile is already in another location.
	switch p.Location {
	case profile.LocationLive:
		if !flagForce {
			return fmt.Errorf("profile %q is currently live in ~/.claude/\n"+
				"  Launching it in an isolate would fork OAuth refresh-token ownership —\n"+
				"  one side will hit 401 when the other rotates.\n"+
				"  Options: close any live claude first, swap away, or use --force.", name)
		}
		fmt.Fprintf(cmd.ErrOrStderr(),
			"Warning: launching %q while it's also live in ~/.claude/ — one side will break.\n", name)
	case profile.LocationIsolated:
		// Reconcile's orphan cleanup already downgraded any dead isolate.
		// If still marked isolated, it's owned by a running claude process.
		if !flagForce {
			return fmt.Errorf("profile %q is already launched (isolate session running)\n"+
				"  A second launch of the same profile would fork token ownership.\n"+
				"  Close the existing session first, or use --force.", name)
		}
		fmt.Fprintf(cmd.ErrOrStderr(),
			"Warning: launching %q when already isolated — one side will break.\n", name)
	}

	// Mark isolated pre-emptively so concurrent launches see the state
	// change as soon as we save.
	p.Location = profile.LocationIsolated
	p.LastUsedAt = timeNow()
	if err := store.Save(storePath); err != nil {
		return fmt.Errorf("save store: %w", err)
	}

	// Release the lock BEFORE materialize + exec. Materialize writes only
	// to isolate/<name>/; the "isolated" marker we just saved prevents
	// other processes from racing on this profile.
	_ = release()
	lockReleased = true

	// Materialize the isolate directory (idempotent).
	if _, err := launch.Materialize(profileDir, isolateDir, claudeConfigHome, isolated); err != nil {
		return fmt.Errorf("materialize isolate: %w", err)
	}

	fmt.Fprintf(cmd.ErrOrStderr(), "Launching claude with profile %q (CLAUDE_CONFIG_DIR=%s)\n",
		name, isolateDir)

	// Exec replaces this process — defers above have already run.
	return launch.Exec("", isolateDir, claudeArgs)
}
