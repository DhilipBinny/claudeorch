package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DhilipBinny/claudeorch/internal/fsio"
	"github.com/DhilipBinny/claudeorch/internal/paths"
	"github.com/DhilipBinny/claudeorch/internal/profile"
	"github.com/DhilipBinny/claudeorch/internal/schema"
	"github.com/spf13/cobra"
)

func init() {
	registerSubcommand(func(root *cobra.Command) {
		root.AddCommand(newUninstallCmd())
	})
}

func newUninstallCmd() *cobra.Command {
	var yes bool
	var keepBinary bool
	var keepState bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove claudeorch state, statusline wiring, and the binary itself.",
		Long: `Uninstalls claudeorch from this machine.

Steps (in order):
  1. Zero-overwrite every credentials.json under ~/.claudeorch/ and remove
     the directory.
  2. Remove the claudeorch statusLine entry from ~/.claude/settings.json
     if we configured it there. Non-claudeorch statusLines are left alone.
  3. Remove the claudeorch binary itself (the one running this command).

~/.claude/ is NEVER touched — your Claude Code login stays intact.

Flags:
  --yes           Skip the interactive confirmation (non-TTY requires this).
  --keep-binary   Wipe state + statusline, keep the binary installed.
  --keep-state    Remove the binary only; leave ~/.claudeorch/ + statusline.

After uninstall, you can reinstall any time:
  curl -fsSL https://raw.githubusercontent.com/DhilipBinny/claudeorch/main/install.sh | sh`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUninstall(cmd, yes, keepBinary, keepState)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the interactive confirmation")
	cmd.Flags().BoolVar(&keepBinary, "keep-binary", false, "remove state + statusline only; keep the binary")
	cmd.Flags().BoolVar(&keepState, "keep-state", false, "remove the binary only; keep ~/.claudeorch/ and statusline")
	return cmd
}

func runUninstall(cmd *cobra.Command, yes, keepBinary, keepState bool) error {
	if keepBinary && keepState {
		return fmt.Errorf("--keep-binary and --keep-state together is a no-op — nothing to do")
	}

	errOut := cmd.ErrOrStderr()
	out := cmd.OutOrStdout()

	orchHome, err := paths.ClaudeorchHome()
	if err != nil {
		return err
	}

	exePath, exeErr := os.Executable()
	if exeErr == nil {
		if resolved, evalErr := filepath.EvalSymlinks(exePath); evalErr == nil {
			exePath = resolved
		}
	}

	// Show the user which account they'll be "stuck" as after uninstall —
	// ~/.claude/ stays intact, but without claudeorch they can't swap.
	// If the account they actually want as the default isn't current, they
	// should cancel and 'swap' first.
	printUninstallContext(errOut)

	fmt.Fprintln(errOut, "Uninstall will:")
	if !keepState {
		fmt.Fprintf(errOut, "  • zero-overwrite + remove %s\n", orchHome)
		fmt.Fprintln(errOut, "  • remove 'statusLine' from ~/.claude/settings.json (only if configured by claudeorch)")
	}
	if !keepBinary && exeErr == nil {
		fmt.Fprintf(errOut, "  • remove the binary %s\n", exePath)
	}
	fmt.Fprintln(errOut, "  • LEAVE ~/.claude/ untouched (your Claude Code login stays intact)")
	fmt.Fprintln(errOut)

	if !yes {
		if !stdinIsTerminal() {
			return fmt.Errorf("uninstall requires interactive confirmation (or --yes for non-interactive)")
		}
		fmt.Fprint(errOut, "Type 'uninstall' to confirm: ")
		reader := bufio.NewReader(os.Stdin)
		line, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("read confirmation: %w", err)
		}
		if strings.TrimSpace(line) != "uninstall" {
			fmt.Fprintln(out, "Uninstall cancelled.")
			return nil
		}
	}

	// 1. State removal (same flow as purge).
	if !keepState {
		if err := removeStatuslineEntry(errOut); err != nil {
			fmt.Fprintf(errOut, "Warning: could not clean statusLine entry: %v\n", err)
		}
		if _, statErr := os.Stat(orchHome); statErr == nil {
			if err := zeroOverwriteCredentials(orchHome); err != nil {
				fmt.Fprintf(errOut, "Warning: zero-overwrite partial: %v\n", err)
			}
			if err := os.RemoveAll(orchHome); err != nil {
				return fmt.Errorf("remove %s: %w", orchHome, err)
			}
			fmt.Fprintf(out, "Removed %s\n", orchHome)
		}
	}

	// 2. Binary removal — do this LAST so we can still print messages.
	// os.Remove on the running binary works on POSIX: the inode stays alive
	// until this process exits, but the file entry disappears from the
	// filesystem immediately. The process then exits normally.
	if !keepBinary && exeErr == nil {
		if err := os.Remove(exePath); err != nil {
			return fmt.Errorf("remove binary %s: %w", exePath, err)
		}
		fmt.Fprintf(out, "Removed binary %s\n", exePath)
	}

	fmt.Fprintln(out, "\nUninstall complete.")
	if !keepBinary {
		fmt.Fprintln(out, "To reinstall: curl -fsSL https://raw.githubusercontent.com/DhilipBinny/claudeorch/main/install.sh | sh")
	}
	return nil
}

// removeStatuslineEntry loads ~/.claude/settings.json, removes the statusLine
// entry if and only if it was configured by claudeorch, and writes back. Other
// settings are preserved. Missing settings.json is a no-op.
func removeStatuslineEntry(errOut io.Writer) error {
	claudeHome, err := paths.ClaudeConfigHome()
	if err != nil {
		return err
	}
	settingsPath := filepath.Join(claudeHome, "settings.json")

	info, statErr := os.Stat(settingsPath)
	if os.IsNotExist(statErr) {
		return nil
	}
	if statErr != nil {
		return statErr
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return fmt.Errorf("parse settings.json: %w", err)
	}
	existing, ok := settings["statusLine"].(map[string]any)
	if !ok {
		return nil
	}
	curr, _ := existing["command"].(string)
	if !strings.Contains(curr, "claudeorch statusline") {
		fmt.Fprintf(errOut, "Note: ~/.claude/settings.json has a non-claudeorch statusLine — leaving it alone.\n")
		return nil
	}
	delete(settings, "statusLine")
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	if err := fsio.WriteFileAtomic(settingsPath, out, info.Mode().Perm()); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	return nil
}

// printUninstallContext shows the user which account they'll be locked into
// after uninstall, plus any other saved profiles. Lets them cancel and swap
// before running the destructive step.
func printUninstallContext(out io.Writer) {
	fmt.Fprintln(out, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Fprintln(out, "Before you uninstall, read this:")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "  ~/.claude/ stays exactly as it is now. Whatever account")
	fmt.Fprintln(out, "  is currently logged in there is the account you'll be")
	fmt.Fprintln(out, "  STUCK with until you 'claude /login' as someone else.")
	fmt.Fprintln(out, "")

	// Read live ~/.claude.json to discover current identity.
	liveEmail := "(unknown)"
	if claudePath, err := paths.ClaudeJSONPath(); err == nil {
		if data, err := os.ReadFile(claudePath); err == nil {
			if id, err := schema.ExtractIdentity(data); err == nil {
				liveEmail = id.EmailAddress
			}
		}
	}
	fmt.Fprintf(out, "  Current live account in ~/.claude/:  %s\n", liveEmail)

	// Show saved profiles + which one matches live (if any).
	storePath, err := paths.StoreFile()
	if err == nil {
		if store, err := profile.Load(storePath); err == nil && len(store.Profiles) > 0 {
			names := make([]string, 0, len(store.Profiles))
			for n := range store.Profiles {
				names = append(names, n)
			}
			sort.Strings(names)

			fmt.Fprintln(out, "")
			fmt.Fprintln(out, "  Saved profiles in claudeorch (will be DELETED):")
			for _, n := range names {
				p := store.Profiles[n]
				marker := "  "
				if store.Active != nil && *store.Active == n {
					marker = "* "
				}
				fmt.Fprintf(out, "    %s%-14s %s\n", marker, n, p.Email)
			}
			if store.Active != nil {
				active := *store.Active
				if p, ok := store.Profiles[active]; ok && p.Email != liveEmail {
					fmt.Fprintln(out, "")
					fmt.Fprintf(out, "  ⚠ Your store says active=%s (%s), but ~/.claude/ actually\n",
						active, p.Email)
					fmt.Fprintf(out, "    holds %s. Something changed outside claudeorch.\n", liveEmail)
				}
			}

			fmt.Fprintln(out, "")
			fmt.Fprintln(out, "  If you want a DIFFERENT saved profile to be your default")
			fmt.Fprintln(out, "  after uninstall, cancel now and run first:")
			fmt.Fprintln(out, "    claudeorch swap <name>")
			fmt.Fprintln(out, "  Then re-run 'claudeorch uninstall'.")
		}
	}
	fmt.Fprintln(out, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Fprintln(out, "")
}
