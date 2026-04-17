package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DhilipBinny/claudeorch/internal/paths"
	"github.com/spf13/cobra"
)

func init() {
	registerSubcommand(func(root *cobra.Command) {
		root.AddCommand(newPurgeCmd())
	})
}

func newPurgeCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "purge",
		Short: "Remove all claudeorch data and zero-overwrite all credentials.",
		Long: `Purge wipes ~/.claudeorch/ entirely:
  - Zero-overwrites every credentials.json before deletion.
  - Removes all profile dirs, isolate dirs, store.json, and lock files.
  - Never touches ~/.claude/ (the live Claude Code directory).

This operation is IRREVERSIBLE. You will need to 'claudeorch add' all
profiles again after purge.

Interactive confirmation required unless --force --yes is given.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPurge(cmd, yes)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip confirmation (requires --force)")
	return cmd
}

func runPurge(cmd *cobra.Command, yes bool) error {
	orchHome, err := paths.ClaudeorchHome()
	if err != nil {
		return err
	}

	// Safety: confirm the path is under a reasonable root (not / or /home).
	if orchHome == "/" || orchHome == os.Getenv("HOME") {
		return fmt.Errorf("purge: refusing to purge suspicious path %q", orchHome)
	}

	// Interactive confirmation unless --force --yes.
	if !(flagForce && yes) {
		if !stdinIsTerminal() {
			return fmt.Errorf("purge requires interactive confirmation (or --force --yes for non-interactive)")
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "This will PERMANENTLY delete all claudeorch data at:\n  %s\n\n", orchHome)
		fmt.Fprintf(cmd.ErrOrStderr(), "Type 'purge' to confirm: ")
		reader := bufio.NewReader(os.Stdin)
		line, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("read confirmation: %w", err)
		}
		if strings.TrimSpace(line) != "purge" {
			fmt.Fprintln(cmd.OutOrStdout(), "Purge cancelled.")
			return nil
		}
	}

	// Zero-overwrite all credential files before deletion.
	if err := zeroOverwriteCredentials(orchHome); err != nil {
		// Non-fatal: continue with deletion.
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: zero-overwrite partial: %v\n", err)
	}

	if err := os.RemoveAll(orchHome); err != nil {
		return fmt.Errorf("purge: remove %s: %w", orchHome, err)
	}

	fmt.Fprintln(cmd.OutOrStdout(), "Purge complete. All claudeorch data has been removed.")
	return nil
}

// zeroOverwriteCredentials walks orchHome and zero-overwrites any
// credentials.json files found.
func zeroOverwriteCredentials(orchHome string) error {
	return filepath.Walk(orchHome, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if filepath.Base(path) == "credentials.json" || filepath.Base(path) == ".credentials.json" {
			zeros := make([]byte, info.Size())
			_ = os.WriteFile(path, zeros, 0o600)
		}
		return nil
	})
}
