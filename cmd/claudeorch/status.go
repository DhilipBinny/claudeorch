package main

import (
	"fmt"
	"path/filepath"

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
			fmt.Fprintf(out, "  terminal  pid=%d  profile=%s  cwd=%s\n",
				s.PID, profileLabelForPID(store, s.PID), s.CWD)
		}
		for _, ide := range ides {
			fmt.Fprintf(out, "  ide(%s)  pid=%d  profile=%s\n",
				ide.IDEName, ide.PID, profileLabelForPID(store, ide.PID))
		}
	}
	return nil
}

// profileLabelForPID determines which saved profile a running claude session
// is using by reading its CLAUDE_CONFIG_DIR env var. Returns:
//
//   - "<name> (launched)" when the env points inside claudeorch's isolate dir
//   - "<active-name>"     when the env is unset → default ~/.claude/
//   - "(external)"        when the env points elsewhere (not a claudeorch dir)
//   - "(unknown)"         when we can't read the process env (macOS, other user)
func profileLabelForPID(store *profile.Store, pid int) string {
	configDir := session.ConfigDirForPID(pid)
	if configDir == "" {
		if store.Active != nil {
			return *store.Active
		}
		return "(unknown)"
	}
	configDir = filepath.Clean(configDir)
	isolatesRoot, err := paths.IsolatesRoot()
	if err != nil {
		return "(external)"
	}
	isolatesRoot = filepath.Clean(isolatesRoot)
	prefix := isolatesRoot + string(filepath.Separator)
	if len(configDir) <= len(prefix) || configDir[:len(prefix)] != prefix {
		return "(external)"
	}
	rel := configDir[len(prefix):]
	// Profile name is the first path segment.
	name := rel
	for i := 0; i < len(rel); i++ {
		if rel[i] == filepath.Separator {
			name = rel[:i]
			break
		}
	}
	if _, known := store.Profiles[name]; known {
		return name + " (launched)"
	}
	return "(external)"
}
