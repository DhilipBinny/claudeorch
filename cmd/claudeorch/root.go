package main

import (
	"fmt"
	"log/slog"
	"os"

	clog "github.com/DhilipBinny/claudeorch/internal/log"
	"github.com/DhilipBinny/claudeorch/internal/paths"
	"github.com/spf13/cobra"
)

// Persistent (global) flag values. Populated by Cobra before any command's RunE.
//
// Per-command-only flags (e.g., --isolated for launch, --no-usage for list) live
// on their respective command definitions, not here.
var (
	flagDebug   bool
	flagJSON    bool
	flagNoColor bool
	flagForce   bool
)

// NoColor reports whether color output should be suppressed.
//
// Suppressed if any of: --no-color flag, NO_COLOR env var set to a non-empty
// value (per the spec at https://no-color.org/ — empty string is NOT activation),
// or stdout is not a terminal (piped).
// The TTY check is added in the UI package when it lands.
func NoColor() bool {
	if flagNoColor {
		return true
	}
	if v := os.Getenv("NO_COLOR"); v != "" {
		return true
	}
	return false
}

// Debug reports whether debug-level logging is enabled.
//
// Enabled if --debug is set OR CLAUDEORCH_DEBUG is set to any non-empty value.
func Debug() bool {
	if flagDebug {
		return true
	}
	if v, set := os.LookupEnv("CLAUDEORCH_DEBUG"); set && v != "" {
		return true
	}
	return false
}

// newRootCmd constructs the top-level claudeorch command with all persistent flags wired.
//
// Subcommands are attached by their respective files in this package via init().
// The command tree is assembled at program startup so that newRootCmd can be
// called multiple times in tests without global-state leakage between runs.
func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "claudeorch",
		Short: "Orchestrate multiple Claude Code accounts from one binary.",
		Long: `claudeorch manages multiple Claude Code accounts on one machine.

It supports two switching modes:
  swap     Replace ~/.claude/ credentials atomically (single active account).
  launch   Exec 'claude' with CLAUDE_CONFIG_DIR pointed at a per-account dir
           (supports parallel isolated sessions in separate terminals).

Plus usage monitoring, profile management, and diagnostics.

Run 'claudeorch --help' or 'claudeorch <command> --help' for details.`,
		Version:       fmt.Sprintf("%s (commit: %s, built: %s)", Version, Commit, BuildDate),
		SilenceUsage:  true, // Don't print usage on error — errors speak for themselves.
		SilenceErrors: true, // We print our own errors; Cobra shouldn't double-print.
		// When invoked without a subcommand, print help rather than doing nothing.
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.SetVersionTemplate("claudeorch {{.Version}}\n")

	flags := cmd.PersistentFlags()
	flags.BoolVar(&flagDebug, "debug", false,
		"verbose logging to stderr and ~/.claudeorch/log/ (also enabled by CLAUDEORCH_DEBUG=1)")
	flags.BoolVar(&flagJSON, "json", false,
		"machine-readable output (where applicable)")
	flags.BoolVar(&flagNoColor, "no-color", false,
		"disable ANSI colors in output (also respects NO_COLOR)")
	flags.BoolVar(&flagForce, "force", false,
		"override safety checks (e.g., swap during an active session) — prints a warning")

	cmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		logFile := ""
		if logPath, err := paths.LogFile(); err == nil {
			logFile = logPath
		}
		_, _, err := clog.Setup(clog.Options{
			Debug:   Debug(),
			LogFile: logFile,
			Stderr:  os.Stderr,
		})
		if err != nil {
			// Non-fatal: log setup failure should never block a command.
			fmt.Fprintf(os.Stderr, "warning: log setup: %v\n", err)
		}
		slog.Debug("claudeorch starting", "cmd", cmd.Name(), "debug", Debug())
		return nil
	}

	// Subcommands are registered by other files in this package.
	registerSubcommands(cmd)

	return cmd
}

// subcommandRegistrars is populated by init() functions in files that define
// subcommands (add.go, list.go, swap.go, ...). Each registrar receives the
// root command and attaches one or more subcommands.
var subcommandRegistrars []func(root *cobra.Command)

// registerSubcommand is called by each subcommand file's init() to request
// attachment during newRootCmd().
func registerSubcommand(f func(root *cobra.Command)) {
	subcommandRegistrars = append(subcommandRegistrars, f)
}

// registerSubcommands runs every registered registrar against the root command.
func registerSubcommands(root *cobra.Command) {
	for _, f := range subcommandRegistrars {
		f(root)
	}
}
