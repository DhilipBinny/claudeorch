package main

import (
	"fmt"

	"github.com/DhilipBinny/claudeorch/internal/paths"
	"github.com/DhilipBinny/claudeorch/internal/profile"
	"github.com/DhilipBinny/claudeorch/internal/session"
	"github.com/spf13/cobra"
)

func init() {
	registerSubcommand(func(root *cobra.Command) {
		root.AddCommand(newStatusCmd())
	})
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "status",
		Short:         "Show the active profile and any running Claude Code sessions.",
		RunE:          runStatus,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
}

func runStatus(cmd *cobra.Command, args []string) error {
	storePath, err := paths.StoreFile()
	if err != nil {
		return err
	}
	store, err := profile.Load(storePath)
	if err != nil {
		return fmt.Errorf("load store: %w", err)
	}

	out := cmd.OutOrStdout()

	// Active profile.
	if store.Active == nil {
		fmt.Fprintln(out, "Active profile: (none)")
	} else {
		p := store.Profiles[*store.Active]
		fmt.Fprintf(out, "Active profile: %s (%s)\n", *store.Active, p.Email)
	}

	// Running sessions.
	claudeConfigHome, err := paths.ClaudeConfigHome()
	if err != nil {
		return err
	}
	sessions, ides, err := session.Sessions(claudeConfigHome)
	if err != nil {
		fmt.Fprintf(out, "Sessions: (error reading: %v)\n", err)
		return nil
	}

	total := len(sessions) + len(ides)
	if total == 0 {
		fmt.Fprintln(out, "Sessions: (none)")
	} else {
		fmt.Fprintf(out, "Sessions: %d running\n", total)
		for _, s := range sessions {
			fmt.Fprintf(out, "  terminal  pid=%d  cwd=%s\n", s.PID, s.CWD)
		}
		for _, ide := range ides {
			fmt.Fprintf(out, "  ide(%s)  pid=%d\n", ide.IDEName, ide.PID)
		}
	}
	return nil
}
