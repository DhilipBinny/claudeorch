package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DhilipBinny/claudeorch/internal/fsio"
	"github.com/DhilipBinny/claudeorch/internal/paths"
	"github.com/DhilipBinny/claudeorch/internal/profile"
	"github.com/spf13/cobra"
)

func init() {
	registerSubcommand(func(root *cobra.Command) {
		root.AddCommand(newRemoveCmd())
	})
}

func newRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "remove <name>",
		Aliases:       []string{"rm", "delete"},
		Short:         "Remove a saved profile and its stored credentials.",
		Args:          cobra.ExactArgs(1),
		RunE:          runRemove,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
}

func runRemove(cmd *cobra.Command, args []string) error {
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

	if _, ok := store.Profiles[name]; !ok {
		return fmt.Errorf("profile %q not found", name)
	}

	// Refuse if active, unless --force.
	if store.IsActive(name) && !flagForce {
		return fmt.Errorf("profile %q is currently active; use --force to remove it anyway", name)
	}

	// Zero-overwrite then delete the profile dir.
	profileDir, err := paths.ProfileDir(name)
	if err != nil {
		return err
	}
	if err := zeroOverwriteDir(profileDir); err != nil {
		return fmt.Errorf("zero-overwrite %s: %w", profileDir, err)
	}
	if err := os.RemoveAll(profileDir); err != nil {
		return fmt.Errorf("remove profile dir %s: %w", profileDir, err)
	}

	// Also wipe the isolate dir if it exists — it holds a copy of the
	// credentials from the most recent 'launch', and leaving it on disk
	// would leak tokens after the user explicitly asked for removal.
	if isolateDir, ierr := paths.IsolateDir(name); ierr == nil {
		if _, statErr := os.Stat(isolateDir); statErr == nil {
			if err := zeroOverwriteDir(isolateDir); err != nil {
				// Log but don't fail — the primary removal already succeeded.
				fmt.Fprintf(cmd.ErrOrStderr(),
					"Warning: zero-overwrite isolate dir %s: %v\n", isolateDir, err)
			}
			if err := os.RemoveAll(isolateDir); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"Warning: remove isolate dir %s: %v\n", isolateDir, err)
			}
		}
	}

	// Clear active if it was this profile.
	if store.IsActive(name) {
		if err := store.SetActive(""); err != nil {
			return err
		}
	}
	delete(store.Profiles, name)

	if err := store.Save(storePath); err != nil {
		return fmt.Errorf("save store: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Removed profile %q\n", name)
	return nil
}

// zeroOverwriteDir overwrites all regular files in dir with zero bytes before
// deletion, making credential recovery harder.
func zeroOverwriteDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.Type().IsRegular() {
			path := filepath.Join(dir, e.Name())
			info, err := e.Info()
			if err != nil {
				continue
			}
			zeros := make([]byte, info.Size())
			// Best-effort: ignore errors (file may already be gone).
			_ = os.WriteFile(path, zeros, 0o600)
		}
	}
	return nil
}
