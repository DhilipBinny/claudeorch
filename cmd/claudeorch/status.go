package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/DhilipBinny/claudeorch/internal/fsio"
	"github.com/DhilipBinny/claudeorch/internal/paths"
	"github.com/DhilipBinny/claudeorch/internal/profile"
	"github.com/DhilipBinny/claudeorch/internal/session"
	"github.com/DhilipBinny/claudeorch/internal/ui"
	"github.com/DhilipBinny/claudeorch/internal/usage"
	"github.com/spf13/cobra"
)

func init() {
	registerSubcommand(func(root *cobra.Command) {
		root.AddCommand(newStatusCmd())
	})
}

func newStatusCmd() *cobra.Command {
	var noUsage bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the active profile, its usage, and running Claude Code sessions.",
		Long: `Prints a "right now" summary for the active profile:

  - Active profile name + email
  - 5-hour and 7-day usage bars for the active profile (one API call)
  - Running claude sessions, each tagged with the profile it's using
  - Footer teaser when other saved profiles exist

For the full table across every saved profile, use 'claudeorch list'.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd, noUsage)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.Flags().BoolVar(&noUsage, "no-usage", false, "skip the usage API call for the active profile")
	return cmd
}

func runStatus(cmd *cobra.Command, noUsage bool) error {
	ui.Init(NoColor())

	// Reconcile before reading — same rationale as list: if Claude Code
	// rotated the active profile's tokens in ~/.claude/, status would show
	// "usage: unavailable" because the profile copy's access token is stale.
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

	storePath, err := paths.StoreFile()
	if err != nil {
		_ = release()
		return err
	}
	store, err := profile.Load(storePath)
	if err != nil {
		_ = release()
		return fmt.Errorf("load store: %w", err)
	}

	rep, reconcileErr := reconcileProfiles(store, cmd.ErrOrStderr())
	if reconcileErr == nil && rep.Changed() {
		_ = store.Save(storePath)
	}
	_ = release()
	// Lock released — the rest is read-only.

	out := cmd.OutOrStdout()

	// ── Active profile + its usage ──────────────────────────────────────
	if store.Active == nil {
		fmt.Fprintln(out, "Active profile: (none)")
		if len(store.Profiles) > 0 {
			fmt.Fprintf(out, "  %d saved profile%s. Run 'claudeorch list' to see them.\n",
				len(store.Profiles), pluralS(len(store.Profiles)))
		}
	} else {
		active := *store.Active
		p := store.Profiles[active]
		fmt.Fprintf(out, "Active profile: %s (%s)\n", active, p.Email)
		if !noUsage {
			accessToken, refreshed, tokenErr := freshAccessToken(active, store, storePath)
			if u, err := fetchUsageWithToken(accessToken, tokenErr); err == nil {
				renderUsageLines(out, u)
			} else {
				fmt.Fprintf(out, "  usage: (unavailable: %v)\n", firstLine(err.Error()))
			}
			// Save only if an actual OAuth refresh happened.
			if refreshed {
				if release2, lockErr := fsio.AcquireLock(context.Background(), lockPath); lockErr == nil {
					_ = store.Save(storePath)
					_ = release2()
				}
			}
		}
	}

	fmt.Fprintln(out)

	// ── Running sessions ────────────────────────────────────────────────
	claudeConfigHome, err := paths.ClaudeConfigHome()
	if err != nil {
		return err
	}
	sessions, ides, sessErr := session.Sessions(claudeConfigHome)
	if sessErr != nil {
		fmt.Fprintf(out, "Sessions: (error reading: %v)\n", sessErr)
		return nil
	}
	total := len(sessions) + len(ides)
	if total == 0 {
		fmt.Fprintln(out, "Sessions: (none)")
	} else {
		fmt.Fprintf(out, "Sessions: %d running\n", total)
		for _, s := range sessions {
			fmt.Fprintf(out, "  terminal     pid=%d  profile=%s  cwd=%s\n",
				s.PID, profileLabelForPID(store, s.PID), s.CWD)
		}
		for _, ide := range ides {
			fmt.Fprintf(out, "  ide(%s)  pid=%d  profile=%s\n",
				ide.IDEName, ide.PID, profileLabelForPID(store, ide.PID))
		}
	}

	// ── Footer: tease 'list' when other profiles exist ─────────────────
	if store.Active != nil {
		others := len(store.Profiles) - 1
		if others > 0 {
			fmt.Fprintf(out, "\n%d other profile%s. Run 'claudeorch list' for all usage.\n",
				others, pluralS(others))
		}
	}

	return nil
}

// fetchUsageWithToken is shared between list.go and status.go — defined
// in list.go to avoid duplication.

// renderUsageLines writes two indented bar lines for the active profile's
// 5-hour and 7-day usage windows.
func renderUsageLines(w io.Writer, u *usage.Usage) {
	fmt.Fprintf(w, "  5H  %s  %3d%%  resets %s\n",
		ui.Bar(u.FiveHour.Percent),
		int(u.FiveHour.Percent*100+0.5),
		resetLabel(u.FiveHour.ResetsAt))
	fmt.Fprintf(w, "  7D  %s  %3d%%  resets %s\n",
		ui.Bar(u.SevenDay.Percent),
		int(u.SevenDay.Percent*100+0.5),
		resetLabel(u.SevenDay.ResetsAt))
}

// resetLabel formats an absolute reset time as a short "in X" string, or
// "-" when the server didn't provide one. Uses the days-aware formatter
// shared with 'list' so 148h becomes "6d4h" instead of "148h12m".
func resetLabel(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return formatDuration(time.Until(t))
}

// firstLine collapses a multi-line error to its first line so the "usage:
// (unavailable: ...)" hint doesn't wrap the terminal with an HTTP body dump.
func firstLine(s string) string {
	for i, c := range s {
		if c == '\n' {
			return s[:i]
		}
	}
	return s
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
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
