package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/DhilipBinny/claudeorch/internal/mux"
	"github.com/DhilipBinny/claudeorch/internal/paths"
	"github.com/DhilipBinny/claudeorch/internal/profile"
	"github.com/DhilipBinny/claudeorch/internal/ui"
	"github.com/spf13/cobra"
)

func init() {
	registerSubcommand(func(root *cobra.Command) {
		root.AddCommand(newMuxCmd())
	})
}

func newMuxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "mux",
		Aliases: []string{"tmux"},
		Short:   "Manage tmux-based Claude Code sessions across profiles.",
		Long: `Launch and control multiple Claude Code instances in tmux sessions.

Each session is tied to a profile and can have multiple windows, each in a
different working directory. Sessions are prefixed with "co-" in tmux to
avoid collisions with your own tmux sessions.

Quick start:
  corch mux start mywork --profile bala --cwd ~/dev/project
  corch mux send mywork:1 "fix the tests"
  corch mux peek mywork
  corch mux attach mywork`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.AddCommand(newMuxStartCmd())
	cmd.AddCommand(newMuxListCmd())
	cmd.AddCommand(newMuxSendCmd())
	cmd.AddCommand(newMuxPeekCmd())
	cmd.AddCommand(newMuxAttachCmd())
	cmd.AddCommand(newMuxStopCmd())
	cmd.AddCommand(newMuxSaveCmd())
	cmd.AddCommand(newMuxLoadCmd())
	cmd.AddCommand(newMuxWorkflowsCmd())
	cmd.AddCommand(newMuxDeleteCmd())

	return cmd
}

// ── start ──────────────────────────────────────────────────────────────

func newMuxStartCmd() *cobra.Command {
	var (
		profileFlag string
		cwdFlags    []string
		windowsFlag int
		agentsFlag  bool
		extraFlag   string
	)

	cmd := &cobra.Command{
		Use:   "start <session> [flags]",
		Short: "Create a tmux session or add windows to an existing one.",
		Long: `Creates a new tmux session with the given name and profile, or adds
windows to an existing session.

--profile is required for new sessions. When adding windows to an existing
session, the profile is inherited automatically.

--cwd can be repeated to open multiple windows in different directories.
When multiple --cwd values are given, --windows is ignored.`,
		Example: `  corch mux start work --profile claude-corp --cwd ~/dev/project
  corch mux start work --cwd ~/dev/repo1 --cwd ~/dev/repo2
  corch mux start work --windows 3
  corch mux start agents --profile bala --agents`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			extra := extraFlag
			if agentsFlag {
				extra = strings.TrimSpace("-- agents " + extra)
			}
			return runMuxStart(cmd, args[0], profileFlag, cwdFlags, windowsFlag, extra)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.Flags().StringVarP(&profileFlag, "profile", "p", "", "claudeorch profile to use")
	cmd.Flags().StringArrayVarP(&cwdFlags, "cwd", "d", nil, "working directory (repeatable)")
	cmd.Flags().IntVarP(&windowsFlag, "windows", "w", 1, "number of windows to create")
	cmd.Flags().BoolVarP(&agentsFlag, "agents", "a", false, "launch 'claude agents' view")
	cmd.Flags().StringVarP(&extraFlag, "extra", "e", "", "extra arguments passed to claude (e.g. \"--model opus\")")

	return cmd
}

func runMuxStart(cmd *cobra.Command, session, profileName string, cwds []string, windows int, extraArgs string) error {
	if err := mux.EnsureTmux(); err != nil {
		return err
	}

	if len(cwds) > 1 {
		windows = len(cwds)
	}

	exe, err := resolvedExecutable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	cwdFor := func(i int) string {
		if i < len(cwds) {
			return cwds[i]
		}
		if len(cwds) == 1 {
			return cwds[0]
		}
		return ""
	}

	if mux.SessionExists(session) {
		if profileName == "" {
			p, err := mux.GetProfile(session)
			if err != nil {
				return fmt.Errorf("cannot detect profile for session %q — pass --profile", session)
			}
			profileName = p
		}

		winCount, _ := mux.WindowCount(session)
		for w := 0; w < windows; w++ {
			winName := fmt.Sprintf("%s-%d", session, winCount+w+1)
			shellCmd := mux.BuildLaunchCmd(exe, profileName, cwdFor(w), extraArgs)
			if err := mux.AddWindow(session, winName, shellCmd); err != nil {
				return fmt.Errorf("add window: %w", err)
			}
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Added %d window(s) to session %q (profile: %s)\n",
			windows, session, profileName)
	} else {
		if profileName == "" {
			p, err := resolveActiveProfile()
			if err != nil {
				return err
			}
			profileName = p
			fmt.Fprintf(cmd.OutOrStdout(), "Using active profile: %s\n", profileName)
		}

		if err := validateProfile(profileName); err != nil {
			return err
		}

		shellCmd := mux.BuildLaunchCmd(exe, profileName, cwdFor(0), extraArgs)
		winName := fmt.Sprintf("%s-1", session)
		if err := mux.CreateSession(session, profileName, winName, shellCmd); err != nil {
			return fmt.Errorf("create session: %w", err)
		}

		for w := 1; w < windows; w++ {
			winName := fmt.Sprintf("%s-%d", session, w+1)
			shellCmd := mux.BuildLaunchCmd(exe, profileName, cwdFor(w), extraArgs)
			if err := mux.AddWindow(session, winName, shellCmd); err != nil {
				return fmt.Errorf("add window %d: %w", w+1, err)
			}
		}
		_ = mux.SelectWindow(session, 1)

		fmt.Fprintf(cmd.OutOrStdout(), "Created session %q with %d window(s) (profile: %s)\n",
			session, windows, profileName)
	}

	if mux.IsTTY() {
		return mux.Attach(session)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Attach with: corch mux attach %s\n", session)
	return nil
}

// ── list ───────────────────────────────────────────────────────────────

func newMuxListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show all claudeorch tmux sessions and their windows.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMuxList(cmd)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
}

func runMuxList(cmd *cobra.Command) error {
	if err := mux.EnsureTmux(); err != nil {
		return err
	}

	ui.Init(NoColor())
	sessions, err := mux.ListSessions()
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No claudeorch tmux sessions running.")
		return nil
	}

	out := cmd.OutOrStdout()
	for _, s := range sessions {
		profileDisplay := s.Profile
		if profileDisplay == "" {
			profileDisplay = "unknown"
		}
		fmt.Fprintf(out, "━━ %s (profile: %s) ━━\n", s.Name, profileDisplay)
		for _, w := range s.Windows {
			fmt.Fprintf(out, "  %d: %s  %s\n", w.Index, w.Name, w.CWD)
		}
		fmt.Fprintln(out)
	}
	return nil
}

// ── send ───────────────────────────────────────────────────────────────

func newMuxSendCmd() *cobra.Command {
	var allFlag bool

	cmd := &cobra.Command{
		Use:   "send <session>[:<window>] <text>...",
		Short: "Send text to a running Claude instance.",
		Long: `Sends text to a Claude Code instance running in a tmux window.

Specify a window index (e.g., mywork:1) to target a specific window,
or use --all to broadcast to every window in the session.`,
		Example: `  corch mux send mywork:1 "find all TODOs"
  corch mux send --all mywork "what are you working on?"`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMuxSend(cmd, args[0], strings.Join(args[1:], " "), allFlag)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.Flags().BoolVar(&allFlag, "all", false, "send to all windows in the session")

	return cmd
}

func runMuxSend(cmd *cobra.Command, target, text string, all bool) error {
	if err := mux.EnsureTmux(); err != nil {
		return err
	}

	sessionName, windowStr := parseTarget(target)

	if !mux.SessionExists(sessionName) {
		return fmt.Errorf("session %q not found — run 'corch mux list'", sessionName)
	}

	if all {
		sessions, err := mux.ListSessions()
		if err != nil {
			return err
		}
		for _, s := range sessions {
			if s.Name == sessionName {
				for _, w := range s.Windows {
					if err := mux.SendKeys(sessionName, w.Index, text); err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "warning: send to %s:%d failed: %v\n", sessionName, w.Index, err)
					}
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Sent to all %d windows in %q\n", len(s.Windows), sessionName)
				return nil
			}
		}
		return fmt.Errorf("session %q not found", sessionName)
	}

	if windowStr == "" {
		return fmt.Errorf("specify a window (e.g., %s:1) or use --all", sessionName)
	}

	windowIdx, err := strconv.Atoi(windowStr)
	if err != nil {
		return fmt.Errorf("invalid window index %q — use a number (e.g., %s:1)", windowStr, sessionName)
	}

	if err := mux.SendKeys(sessionName, windowIdx, text); err != nil {
		return fmt.Errorf("send to %s:%d: %w", sessionName, windowIdx, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Sent to %s:%d\n", sessionName, windowIdx)
	return nil
}

// ── peek ───────────────────────────────────────────────────────────────

func newMuxPeekCmd() *cobra.Command {
	var linesFlag int

	cmd := &cobra.Command{
		Use:   "peek <session>[:<window>]",
		Short: "Capture and display output from a Claude instance.",
		Long: `Captures the current screen content from a tmux window.

Without a window index, peeks all windows in the session with headers.`,
		Example: `  corch mux peek mywork        # all windows
  corch mux peek mywork:2      # specific window
  corch mux peek mywork --lines 50`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMuxPeek(cmd, args[0], linesFlag)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.Flags().IntVarP(&linesFlag, "lines", "n", 100, "number of lines to capture")

	return cmd
}

func runMuxPeek(cmd *cobra.Command, target string, lines int) error {
	if err := mux.EnsureTmux(); err != nil {
		return err
	}

	sessionName, windowStr := parseTarget(target)

	if !mux.SessionExists(sessionName) {
		return fmt.Errorf("session %q not found", sessionName)
	}

	out := cmd.OutOrStdout()

	if windowStr != "" {
		windowIdx, err := strconv.Atoi(windowStr)
		if err != nil {
			return fmt.Errorf("invalid window index %q", windowStr)
		}
		content, err := mux.CapturePaneOutput(sessionName, windowIdx, lines)
		if err != nil {
			return fmt.Errorf("capture %s:%d: %w", sessionName, windowIdx, err)
		}
		fmt.Fprint(out, content)
		return nil
	}

	sessions, err := mux.ListSessions()
	if err != nil {
		return err
	}
	for _, s := range sessions {
		if s.Name == sessionName {
			for _, w := range s.Windows {
				fmt.Fprintf(out, "━━ %s:%d (%s) ━━\n", sessionName, w.Index, w.Name)
				content, err := mux.CapturePaneOutput(sessionName, w.Index, lines)
				if err != nil {
					fmt.Fprintf(out, "  (error: %v)\n", err)
				} else {
					fmt.Fprint(out, content)
				}
				fmt.Fprintln(out)
			}
			return nil
		}
	}
	return fmt.Errorf("session %q not found", sessionName)
}

// ── attach ─────────────────────────────────────────────────────────────

func newMuxAttachCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "attach <session>",
		Short:   "Attach to a tmux session.",
		Example: "  corch mux attach mywork",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := mux.EnsureTmux(); err != nil {
				return err
			}
			if !mux.SessionExists(args[0]) {
				return fmt.Errorf("session %q not found — run 'corch mux list'", args[0])
			}
			return mux.Attach(args[0])
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
}

// ── stop ───────────────────────────────────────────────────────────────

func newMuxStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <session>[:<window>]",
		Short: "Kill a session or a specific window.",
		Example: `  corch mux stop mywork      # kill entire session
  corch mux stop mywork:2    # kill window 2 only`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMuxStop(cmd, args[0])
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
}

func runMuxStop(cmd *cobra.Command, target string) error {
	if err := mux.EnsureTmux(); err != nil {
		return err
	}

	sessionName, windowStr := parseTarget(target)

	if !mux.SessionExists(sessionName) {
		return fmt.Errorf("session %q not found", sessionName)
	}

	if windowStr != "" {
		windowIdx, err := strconv.Atoi(windowStr)
		if err != nil {
			return fmt.Errorf("invalid window index %q", windowStr)
		}
		if err := mux.KillWindow(sessionName, windowIdx); err != nil {
			return fmt.Errorf("kill window %s:%d: %w", sessionName, windowIdx, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Killed window %s:%d\n", sessionName, windowIdx)
	} else {
		if err := mux.KillSession(sessionName); err != nil {
			return fmt.Errorf("kill session %q: %w", sessionName, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Killed session %q\n", sessionName)
	}

	// Trigger reconcile to clean up orphaned isolate states.
	runReconcileQuiet(cmd)
	return nil
}

// ── helpers ────────────────────────────────────────────────────────────

func parseTarget(target string) (session, window string) {
	if idx := strings.IndexByte(target, ':'); idx >= 0 {
		return target[:idx], target[idx+1:]
	}
	return target, ""
}

func resolveActiveProfile() (string, error) {
	storePath, err := paths.StoreFile()
	if err != nil {
		return "", err
	}
	store, err := profile.Load(storePath)
	if err != nil {
		return "", fmt.Errorf("load store: %w", err)
	}
	if store.Active != nil {
		return *store.Active, nil
	}
	return "", fmt.Errorf("no active profile — pass --profile or run 'corch swap <profile>' first")
}

func validateProfile(name string) error {
	storePath, err := paths.StoreFile()
	if err != nil {
		return err
	}
	store, err := profile.Load(storePath)
	if err != nil {
		return fmt.Errorf("load store: %w", err)
	}
	if _, ok := store.Profiles[name]; !ok {
		return fmt.Errorf("profile %q not found — run 'corch list' to see available profiles", name)
	}
	return nil
}

// ── save ───────────────────────────────────────────────────────────────

func newMuxSaveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "save <name>",
		Short: "Snapshot all running sessions as a named workflow.",
		Long: `Captures the current tmux session layout (sessions, profiles, window
counts, working directories, extra args) and saves it for later replay
with 'corch mux load'.`,
		Example: `  corch mux save dev-setup
  corch mux save morning`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := mux.EnsureTmux(); err != nil {
				return err
			}
			wf, err := mux.SnapshotWorkflow(args[0])
			if err != nil {
				return err
			}
			if err := mux.SaveWorkflow(args[0], *wf); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Saved workflow %q (%d sessions)\n", args[0], len(wf.Sessions))
			for _, s := range wf.Sessions {
				extra := ""
				if s.ExtraArgs != "" {
					extra = fmt.Sprintf("  [%s]", s.ExtraArgs)
				}
				fmt.Fprintf(out, "  %s  profile=%s  windows=%d  cwd=%s%s\n",
					s.Name, s.Profile, s.Windows, s.CWD, extra)
			}
			return nil
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
}

// ── load ───────────────────────────────────────────────────────────────

func newMuxLoadCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "load <name>",
		Short: "Restore sessions from a saved workflow.",
		Long: `Launches all sessions defined in the named workflow. Sessions that
already exist are skipped (not duplicated).`,
		Example: `  corch mux load dev-setup
  corch mux load morning --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := mux.EnsureTmux(); err != nil {
				return err
			}
			wf, err := mux.LoadWorkflow(args[0])
			if err != nil {
				return err
			}
			exe, err := resolvedExecutable()
			if err != nil {
				return fmt.Errorf("resolve executable: %w", err)
			}
			out := cmd.OutOrStdout()
			if dryRun {
				fmt.Fprintf(out, "Workflow %q would launch:\n", wf.Name)
			} else {
				fmt.Fprintf(out, "Loading workflow %q...\n", wf.Name)
			}
			for _, s := range wf.Sessions {
				if mux.SessionExists(s.Name) {
					fmt.Fprintf(out, "  %-15s  SKIP (already running)\n", s.Name)
					continue
				}
				if dryRun {
					extra := ""
					if s.ExtraArgs != "" {
						extra = fmt.Sprintf("  [%s]", s.ExtraArgs)
					}
					fmt.Fprintf(out, "  %-15s  profile=%s  windows=%d  cwd=%s%s\n",
						s.Name, s.Profile, s.Windows, s.CWD, extra)
					continue
				}
				shellCmd := mux.BuildLaunchCmd(exe, s.Profile, s.CWD, s.ExtraArgs)
				winName := fmt.Sprintf("%s-1", s.Name)
				if err := mux.CreateSession(s.Name, s.Profile, winName, shellCmd); err != nil {
					fmt.Fprintf(out, "  %-15s  FAIL: %v\n", s.Name, err)
					continue
				}
				for w := 1; w < s.Windows; w++ {
					wn := fmt.Sprintf("%s-%d", s.Name, w+1)
					sc := mux.BuildLaunchCmd(exe, s.Profile, s.CWD, s.ExtraArgs)
					if err := mux.AddWindow(s.Name, wn, sc); err != nil {
						fmt.Fprintf(out, "  %-15s  window %d FAIL: %v\n", s.Name, w+1, err)
					}
				}
				_ = mux.SelectWindow(s.Name, 1)
				extra := ""
				if s.ExtraArgs != "" {
					extra = fmt.Sprintf("  [%s]", s.ExtraArgs)
				}
				fmt.Fprintf(out, "  %-15s  OK  %d window(s)  profile=%s%s\n",
					s.Name, s.Windows, s.Profile, extra)
			}
			return nil
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be launched without doing it")
	return cmd
}

// ── workflows ─────────────────────────────────────────────────────────

func newMuxWorkflowsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "workflows",
		Aliases: []string{"wf"},
		Short:   "List saved workflows.",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			workflows, err := mux.ListWorkflows()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(workflows) == 0 {
				fmt.Fprintln(out, "No saved workflows. Use 'corch mux save <name>' to create one.")
				return nil
			}
			for _, wf := range workflows {
				totalWin := 0
				for _, s := range wf.Sessions {
					totalWin += s.Windows
				}
				fmt.Fprintf(out, "  %-20s  %d session(s), %d window(s)\n",
					wf.Name, len(wf.Sessions), totalWin)
				for _, s := range wf.Sessions {
					extra := ""
					if s.ExtraArgs != "" {
						extra = fmt.Sprintf("  [%s]", s.ExtraArgs)
					}
					fmt.Fprintf(out, "    %-16s  profile=%-8s  win=%d  %s%s\n",
						s.Name, s.Profile, s.Windows, s.CWD, extra)
				}
			}
			return nil
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
}

// ── delete ─────────────────────────────────────────────────────────────

func newMuxDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "delete <workflow>",
		Aliases: []string{"rm"},
		Short:   "Delete a saved workflow.",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := mux.DeleteWorkflow(args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted workflow %q\n", args[0])
			return nil
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
}

func runReconcileQuiet(cmd *cobra.Command) {
	storePath, err := paths.StoreFile()
	if err != nil {
		return
	}
	store, err := profile.Load(storePath)
	if err != nil {
		return
	}
	if rep, err := reconcileProfiles(store, os.Stderr); err == nil && rep.Changed() {
		_ = store.Save(storePath)
	}
}
