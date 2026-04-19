package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/DhilipBinny/claudeorch/internal/fsio"
	"github.com/DhilipBinny/claudeorch/internal/paths"
	"github.com/DhilipBinny/claudeorch/internal/profile"
	"github.com/DhilipBinny/claudeorch/internal/session"
	swappkg "github.com/DhilipBinny/claudeorch/internal/swap"
	"github.com/spf13/cobra"
)

func init() {
	registerSubcommand(func(root *cobra.Command) {
		root.AddCommand(newSwapCmd())
	})
}

func newSwapCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "swap <name>",
		Short: "Swap the active Claude Code account to a saved profile.",
		Long: `Atomically replaces ~/.claude/.credentials.json and ~/.claude.json with
the saved profile's files.

Refuses if any live Claude Code session is detected (exit 2), unless --force
is given (prints a warning in that case).`,
		Args:          cobra.ExactArgs(1),
		RunE:          runSwap,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
}

func runSwap(cmd *cobra.Command, args []string) error {
	name := args[0]
	if err := paths.ValidateProfileName(name); err != nil {
		return err
	}

	// Verify the profile exists BEFORE checking sessions. Otherwise a typo
	// like 'swap ghsot' is reported as "Claude Code is currently running"
	// when the live-session check fires, which is misleading — the user
	// can't tell if the name is wrong or sessions are in the way.
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

	// Check for live sessions before acquiring lock (fast path).
	claudeConfigHome, err := paths.ClaudeConfigHome()
	if err != nil {
		return err
	}
	alive, err := session.HasLiveSession(claudeConfigHome)
	if err != nil {
		return fmt.Errorf("check sessions: %w", err)
	}
	if alive && !flagForce {
		fmt.Fprintln(cmd.ErrOrStderr(),
			"Error: Claude Code is currently running. Close all sessions before swapping accounts.")
		fmt.Fprintln(cmd.ErrOrStderr(), "Use --force to override (unsafe — may corrupt state).")
		os.Exit(2)
	}
	if alive && flagForce {
		fmt.Fprintln(cmd.ErrOrStderr(),
			"Warning: forcing swap while Claude Code is running. This may corrupt your session.")
	}

	// Acquire global lock.
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
	defer func() { _ = release() }()

	// Re-load store under the lock — the preliminary load was for the fast
	// fail-on-missing-name path; this one is authoritative for the swap.
	store, err = profile.Load(storePath)
	if err != nil {
		return fmt.Errorf("load store: %w", err)
	}
	if _, ok := store.Profiles[name]; !ok {
		return fmt.Errorf("profile %q not found", name)
	}

	// Freshen profile snapshots before swap. The outgoing live account may
	// have been rotated by plain claude usage, and the incoming profile may
	// have been refreshed via a prior isolate session — reconcile catches
	// both and prevents us from copying stale tokens into ~/.claude/.
	if _, err := reconcileProfiles(store, cmd.ErrOrStderr()); err != nil {
		return fmt.Errorf("reconcile: %w", err)
	}

	profileDir, err := paths.ProfileDir(name)
	if err != nil {
		return err
	}
	claudeJSONPath, err := paths.ClaudeJSONPath()
	if err != nil {
		return err
	}
	orchHome, err := paths.ClaudeorchHome()
	if err != nil {
		return err
	}

	if err := swappkg.Run(profileDir, orchHome, claudeConfigHome, claudeJSONPath); err != nil {
		return fmt.Errorf("swap failed: %w", err)
	}

	// Update store active pointer + LastUsedAt.
	if err := store.SetActive(name); err != nil {
		return err
	}
	store.Profiles[name].LastUsedAt = time.Now().UTC()
	if err := store.Save(storePath); err != nil {
		return fmt.Errorf("save store: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Swapped to profile %q (%s)\n", name, store.Profiles[name].Email)
	return nil
}
