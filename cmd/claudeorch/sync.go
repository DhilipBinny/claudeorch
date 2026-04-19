package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/DhilipBinny/claudeorch/internal/fsio"
	"github.com/DhilipBinny/claudeorch/internal/paths"
	"github.com/DhilipBinny/claudeorch/internal/profile"
	"github.com/spf13/cobra"
)

func init() {
	registerSubcommand(func(root *cobra.Command) {
		root.AddCommand(newSyncCmd())
	})
}

func newSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Reconcile saved profiles with live Claude Code state.",
		Long: `Scans every saved profile and promotes the freshest copy of its
credentials into the canonical profile location.

Why run this
  Claude Code silently rotates OAuth tokens on every refresh. The saved
  profile snapshot goes stale within hours of active use. Running 'sync'
  pulls those fresher tokens back into the profile so subsequent 'swap'
  or 'launch' commands see current state.

  Every state-mutating claudeorch command (add/swap/launch/refresh/remove)
  already runs sync implicitly. Invoke this explicitly after a long period
  of plain 'claude' use (hours or days) to make sure profiles are current
  before you rely on them.

What it does
  - Compares credential files across profile/, isolate/, and live ~/.claude/
  - Promotes the one with the latest expiresAt into profile/
  - Corrects the active-profile pointer if ~/.claude/ has drifted
  - Clears orphaned 'isolated' markers (dead claude sessions)
  - Surfaces dangerous double-holder states as warnings

Output
  Reports one line per profile that changed. Silent when nothing changed.`,
		RunE:          runSync,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
}

func runSync(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	errOut := cmd.ErrOrStderr()

	// Take the global flock — reconcile's contract requires it.
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

	storePath, err := paths.StoreFile()
	if err != nil {
		return err
	}
	store, err := profile.Load(storePath)
	if err != nil {
		return fmt.Errorf("load store: %w", err)
	}

	rep, err := reconcileProfiles(store, errOut)
	if err != nil {
		return fmt.Errorf("reconcile: %w", err)
	}

	// Always save. Even when reconcile reports no functional changes,
	// Load() may have upgraded a v1 store.json to v2 in-memory — a save
	// is required to persist that upgrade. Save() is idempotent and cheap.
	if err := store.Save(storePath); err != nil {
		return fmt.Errorf("save store: %w", err)
	}

	nothingNotable := !rep.Changed() &&
		len(rep.IsolatedLiveConflicts) == 0 &&
		!rep.LiveIdentityUnknown &&
		len(rep.DuplicateIdentities) == 0
	if nothingNotable {
		fmt.Fprintln(out, "Already in sync.")
		return nil
	}

	// Per-change summary.
	for _, name := range rep.TokensPromoted {
		fmt.Fprintf(out, "  • refreshed profile/%s from newer source\n", name)
	}
	for _, name := range rep.OrphansCleared {
		fmt.Fprintf(out, "  • cleared orphan isolated marker on profile/%s\n", name)
	}
	if rep.ActiveCorrected {
		if store.Active != nil {
			fmt.Fprintf(out, "  • corrected active pointer to %q\n", *store.Active)
		} else {
			fmt.Fprintln(out, "  • cleared stale active pointer")
		}
	}

	return nil
}
