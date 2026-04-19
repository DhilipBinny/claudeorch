package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/DhilipBinny/claudeorch/internal/creds"
	"github.com/DhilipBinny/claudeorch/internal/fsio"
	"github.com/DhilipBinny/claudeorch/internal/oauth"
	"github.com/DhilipBinny/claudeorch/internal/paths"
	"github.com/DhilipBinny/claudeorch/internal/profile"
	"github.com/spf13/cobra"
)

func init() {
	registerSubcommand(func(root *cobra.Command) {
		root.AddCommand(newRefreshCmd())
	})
}

func newRefreshCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh <name>",
		Short: "Refresh the OAuth token for a saved profile.",
		Long: `Calls the Claude OAuth endpoint to rotate the access and refresh tokens.

Refuses to refresh the currently active profile unless --force is given
(to avoid invalidating a running session's token).

On invalid_grant (token expired or revoked), marks the profile as
needs_reauth=true and exits 1. Run 'claude /login' and 'claudeorch add'
to re-add the profile.`,
		Args:          cobra.ExactArgs(1),
		RunE:          runRefresh,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
}

func runRefresh(cmd *cobra.Command, args []string) error {
	name := args[0]
	if err := paths.ValidateProfileName(name); err != nil {
		return err
	}

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

	// Reconcile first — if the profile's tokens are already up to date in
	// live or isolate, refresh can short-circuit. More importantly, we
	// mustn't refresh using a stale refresh token when a newer one already
	// exists elsewhere on disk.
	if _, err := reconcileProfiles(store, cmd.ErrOrStderr()); err != nil {
		return fmt.Errorf("reconcile: %w", err)
	}

	p, ok := store.Profiles[name]
	if !ok {
		return fmt.Errorf("profile %q not found", name)
	}

	// Refuse to refresh active profile unless --force.
	if store.IsActive(name) && !flagForce {
		return fmt.Errorf("profile %q is active; use --force to refresh it (may interrupt running sessions)", name)
	}

	profileDir, err := paths.ProfileDir(name)
	if err != nil {
		return err
	}
	credsPath := filepath.Join(profileDir, "credentials.json")

	credsData, err := os.ReadFile(credsPath)
	if err != nil {
		return fmt.Errorf("read credentials for %q: %w", name, err)
	}

	newCredsData, err := oauth.Refresh(context.Background(), credsData)
	if err != nil {
		if errors.Is(err, oauth.ErrInvalidGrant) {
			// Mark needs_reauth and persist.
			p.NeedsReauth = true
			p.LastUsedAt = time.Now().UTC()
			if saveErr := store.Save(storePath); saveErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: failed to save needs_reauth flag: %v\n", saveErr)
			}
			fmt.Fprintln(cmd.ErrOrStderr(),
				"Error: refresh token expired or revoked (invalid_grant).")
			fmt.Fprintln(cmd.ErrOrStderr(),
				"Run 'claude /login' to re-authenticate, then 'claudeorch add "+name+"' to update.")
			return fmt.Errorf("%w", oauth.ErrInvalidGrant)
		}
		return fmt.Errorf("refresh failed: %w", err)
	}

	// Write refreshed credentials atomically to the profile copy.
	if err := fsio.WriteFileAtomic(credsPath, newCredsData, 0o600); err != nil {
		return fmt.Errorf("write refreshed credentials: %w", err)
	}

	// If this is the active profile, sync the live ~/.claude/.credentials.json
	// too — otherwise the live file still holds the old refreshToken that
	// Anthropic just revoked on rotation, and Claude Code will stop working on
	// its next refresh attempt.
	if store.IsActive(name) {
		liveCredsPath, lErr := paths.ClaudeCredentialsPath()
		if lErr == nil {
			if writeErr := creds.WriteLive(liveCredsPath, newCredsData); writeErr != nil {
				// Loud warning: profile copy has new tokens, live has old (revoked) ones.
				// User can recover by running 'claudeorch swap <name>' which atomically
				// re-copies the profile credentials into the live location.
				fmt.Fprintf(cmd.ErrOrStderr(),
					"Warning: could not sync live credentials at %s: %v\n"+
						"  The profile has the new tokens, but ~/.claude/ still has the old (revoked) ones.\n"+
						"  Run 'claudeorch swap %s' to resync.\n",
					liveCredsPath, writeErr, name)
			}
		}
	}

	// Update isolate copy if it exists.
	isolateDir, err := paths.IsolateDir(name)
	if err == nil {
		isolateCreds := filepath.Join(isolateDir, ".credentials.json")
		if _, statErr := os.Stat(isolateCreds); statErr == nil {
			_ = fsio.WriteFileAtomic(isolateCreds, newCredsData, 0o600)
		}
	}

	// Update LastUsedAt.
	p.LastUsedAt = time.Now().UTC()
	p.NeedsReauth = false
	if err := store.Save(storePath); err != nil {
		return fmt.Errorf("save store: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Refreshed token for profile %q\n", name)
	return nil
}
