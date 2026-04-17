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
		root.AddCommand(newRenameCmd())
	})
}

func newRenameCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "rename <old-name> <new-name>",
		Short:        "Rename a saved profile.",
		Args:         cobra.ExactArgs(2),
		RunE:         runRename,
		SilenceUsage: true,
	}
}

func runRename(cmd *cobra.Command, args []string) error {
	oldName, newName := args[0], args[1]

	if err := paths.ValidateProfileName(oldName); err != nil {
		return fmt.Errorf("old name: %w", err)
	}
	if err := paths.ValidateProfileName(newName); err != nil {
		return fmt.Errorf("new name: %w", err)
	}
	if oldName == newName {
		return fmt.Errorf("old and new names are the same")
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

	if _, ok := store.Profiles[oldName]; !ok {
		return fmt.Errorf("profile %q not found", oldName)
	}
	if _, ok := store.Profiles[newName]; ok {
		return fmt.Errorf("profile %q already exists", newName)
	}

	oldDir, err := paths.ProfileDir(oldName)
	if err != nil {
		return err
	}
	newDir, err := paths.ProfileDir(newName)
	if err != nil {
		return err
	}

	if err := os.Rename(oldDir, newDir); err != nil {
		return fmt.Errorf("rename profile dir: %w", err)
	}

	p := store.Profiles[oldName]
	p.Name = newName
	store.Profiles[newName] = p
	delete(store.Profiles, oldName)

	// Update active pointer if needed.
	if store.IsActive(oldName) {
		if err := store.SetActive(newName); err != nil {
			return err
		}
	}

	if err := store.Save(storePath); err != nil {
		return fmt.Errorf("save store: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Renamed %q → %q\n", oldName, newName)
	return nil
}
