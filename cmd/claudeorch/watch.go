package main

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/DhilipBinny/claudeorch/internal/mux"
	"github.com/DhilipBinny/claudeorch/internal/ui"
	"github.com/DhilipBinny/claudeorch/internal/watch"
	"github.com/spf13/cobra"
)

func init() {
	registerSubcommand(func(root *cobra.Command) {
		root.AddCommand(newWatchCmd())
	})
}

func newWatchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Monitor tmux sessions and notify when Claude needs attention.",
		Long: `Watches all claudeorch tmux sessions for idle/active state changes
and sends OS notifications when a Claude instance finishes and is waiting
for your input.

Subcommands:
  start    Start the background watcher daemon
  status   Show current state of all sessions
  stop     Stop the background watcher`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.AddCommand(newWatchStartCmd())
	cmd.AddCommand(newWatchStatusCmd())
	cmd.AddCommand(newWatchStopCmd())

	return cmd
}

// ── start ──────────────────────────────────────────────────────────────

func newWatchStartCmd() *cobra.Command {
	var (
		intervalFlag int
		foreground   bool
	)

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the watcher (background by default).",
		Long: `Starts polling all claudeorch tmux sessions every N seconds
(default: 10). When a Claude instance transitions from active to idle,
a desktop notification is sent.

Use --foreground to run in the current terminal (useful for debugging).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWatchStart(cmd, time.Duration(intervalFlag)*time.Second, foreground)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.Flags().IntVar(&intervalFlag, "interval", 10, "poll interval in seconds")
	cmd.Flags().BoolVar(&foreground, "foreground", false, "run in foreground (don't daemonize)")

	return cmd
}

func runWatchStart(cmd *cobra.Command, interval time.Duration, foreground bool) error {
	if err := mux.EnsureTmux(); err != nil {
		return err
	}

	// Check if already running.
	if pid, err := readWatcherPid(); err == nil {
		if processAlive(pid) {
			return fmt.Errorf("watcher already running (pid %d) — run 'corch watch stop' first", pid)
		}
		// Stale PID file — clean up.
		pidPath, _ := watch.PidFilePath()
		_ = os.Remove(pidPath)
	}

	if !foreground {
		return daemonize(cmd, interval)
	}

	return runForeground(cmd, interval)
}

func runForeground(cmd *cobra.Command, interval time.Duration) error {
	pid := os.Getpid()
	if err := writeWatcherPid(pid); err != nil {
		return fmt.Errorf("write pid file: %w", err)
	}
	defer func() {
		pidPath, _ := watch.PidFilePath()
		_ = os.Remove(pidPath)
		statusPath, _ := watch.StatusFilePath()
		_ = os.Remove(statusPath)
	}()

	fmt.Fprintf(cmd.OutOrStdout(), "Watcher started (pid %d, interval %s)\n", pid, interval)

	w := watch.New(interval)

	// Handle signals for graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		fmt.Fprintln(cmd.OutOrStdout(), "\nWatcher stopping...")
		w.Stop()
	}()

	// Periodic status file writes.
	go func() {
		for {
			time.Sleep(interval)
			snap := watch.PollOnce()
			_ = watch.WriteStatus(snap)
		}
	}()

	w.Run()
	return nil
}

func daemonize(cmd *cobra.Command, interval time.Duration) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	args := []string{exe, "watch", "start", "--foreground",
		"--interval", strconv.Itoa(int(interval.Seconds()))}

	attr := &os.ProcAttr{
		Dir:   "/",
		Env:   os.Environ(),
		Files: []*os.File{os.Stdin, nil, nil}, // detach stdout/stderr
	}

	proc, err := os.StartProcess(exe, args, attr)
	if err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	_ = proc.Release()

	fmt.Fprintf(cmd.OutOrStdout(), "Watcher daemon started (pid %d, interval %s)\n", proc.Pid, interval)
	fmt.Fprintf(cmd.OutOrStdout(), "Check status: corch watch status\n")
	fmt.Fprintf(cmd.OutOrStdout(), "Stop:         corch watch stop\n")
	return nil
}

// ── status ─────────────────────────────────────────────────────────────

func newWatchStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show current state of all tmux sessions.",
		Long: `Displays a summary of all claudeorch tmux sessions with their
current state (ACTIVE, IDLE, or UNKNOWN) and how long they've been
in that state.

If the watcher daemon is running, reads from its status file.
Otherwise, performs a one-shot poll.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWatchStatus(cmd)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
}

func runWatchStatus(cmd *cobra.Command) error {
	if err := mux.EnsureTmux(); err != nil {
		return err
	}

	ui.Init(NoColor())
	out := cmd.OutOrStdout()

	// Check if watcher is running.
	watcherRunning := false
	if pid, err := readWatcherPid(); err == nil && processAlive(pid) {
		watcherRunning = true
		fmt.Fprintf(out, "Watcher: running (pid %d)\n\n", pid)
	} else {
		fmt.Fprintln(out, "Watcher: not running\n")
	}

	// Read from status file if available, else poll live.
	var snap watch.Snapshot
	if watcherRunning {
		if saved, err := watch.ReadStatus(); err == nil {
			snap = *saved
		} else {
			snap = watch.PollOnce()
		}
	} else {
		snap = watch.PollOnce()
	}

	if len(snap.Windows) == 0 {
		fmt.Fprintln(out, "No claudeorch tmux sessions running.")
		return nil
	}

	for _, ws := range snap.Windows {
		stateStr := ws.State.String()
		since := ""
		if !ws.Since.IsZero() {
			d := time.Since(ws.Since)
			since = formatWatchDuration(d)
		}
		profileStr := ws.Profile
		if profileStr == "" {
			profileStr = "?"
		}

		var stateColor string
		switch ws.State {
		case watch.StateIdle:
			stateColor = "\033[33m" // yellow
		case watch.StateActive:
			stateColor = "\033[32m" // green
		default:
			stateColor = "\033[90m" // gray
		}
		reset := "\033[0m"
		if NoColor() {
			stateColor = ""
			reset = ""
		}

		fmt.Fprintf(out, "  %-20s %s%-8s%s %-8s [%s]\n",
			fmt.Sprintf("%s:%d", ws.Session, ws.Window),
			stateColor, stateStr, reset,
			since,
			profileStr)
	}

	return nil
}

// ── stop ───────────────────────────────────────────────────────────────

func newWatchStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the background watcher.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWatchStop(cmd)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
}

func runWatchStop(cmd *cobra.Command) error {
	pid, err := readWatcherPid()
	if err != nil {
		return fmt.Errorf("watcher not running (no pid file)")
	}

	if !processAlive(pid) {
		pidPath, _ := watch.PidFilePath()
		_ = os.Remove(pidPath)
		fmt.Fprintln(cmd.OutOrStdout(), "Watcher was not running (stale pid file cleaned).")
		return nil
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find process %d: %w", pid, err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("send SIGTERM to %d: %w", pid, err)
	}

	pidPath, _ := watch.PidFilePath()
	_ = os.Remove(pidPath)
	statusPath, _ := watch.StatusFilePath()
	_ = os.Remove(statusPath)

	fmt.Fprintf(cmd.OutOrStdout(), "Watcher stopped (pid %d)\n", pid)
	return nil
}

// ── pid helpers ────────────────────────────────────────────────────────

func readWatcherPid() (int, error) {
	path, err := watch.PidFilePath()
	if err != nil {
		return 0, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

func writeWatcherPid(pid int) error {
	path, err := watch.PidFilePath()
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strconv.Itoa(pid)), 0o644)
}

func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

func formatWatchDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%dm ago", int(d.Hours()), int(d.Minutes())%60)
}
