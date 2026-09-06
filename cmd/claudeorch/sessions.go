package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DhilipBinny/claudeorch/internal/session"
	"github.com/DhilipBinny/claudeorch/internal/ui"
	"github.com/spf13/cobra"
)

func init() {
	registerSubcommand(func(root *cobra.Command) {
		root.AddCommand(newSessionsCmd())
	})
}

func newSessionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "sessions",
		Aliases: []string{"hist", "history"},
		Short:   "Browse all past Claude conversations across directories.",
		Long: `Scans all project conversation history from ~/.claude/projects/ and
all claudeorch isolate directories to show a unified view of every
Claude Code conversation on this machine.

Subcommands:
  list     List all conversations (default)
  show     Show details of a specific conversation
  clone    Clone a conversation to branch off
  dirs     List directories where Claude was used`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSessionsList(cmd, "", 30)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.AddCommand(newSessionsListCmd())
	cmd.AddCommand(newSessionsDirsCmd())
	cmd.AddCommand(newSessionsShowCmd())
	cmd.AddCommand(newSessionsCloneCmd())

	return cmd
}

func sessionsBaseDirs() (claudeDir, isolateDir string) {
	home, _ := os.UserHomeDir()
	claudeDir = filepath.Join(home, ".claude")
	isolateDir = filepath.Join(home, ".claudeorch", "isolate")
	return
}

// ── list ──────────────────────────────────────────────────────────────

func newSessionsListCmd() *cobra.Command {
	var (
		limitFlag   int
		profileFlag string
		dirFlag     string
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all conversations, most recent first.",
		Example: `  corch sessions list
  corch sessions list --limit 50
  corch sessions list --profile trax
  corch sessions list --dir ~/dev/basement`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSessionsList(cmd, profileFlag, limitFlag)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.Flags().IntVarP(&limitFlag, "limit", "n", 30, "max conversations to show")
	cmd.Flags().StringVar(&profileFlag, "profile", "", "filter by profile")
	cmd.Flags().StringVar(&dirFlag, "dir", "", "filter by directory")

	return cmd
}

func runSessionsList(cmd *cobra.Command, profileFilter string, limit int) error {
	ui.Init(NoColor())
	claudeDir, isolateDir := sessionsBaseDirs()

	convos, err := session.ScanConversations(claudeDir, isolateDir)
	if err != nil {
		return err
	}

	dirFilter, _ := cmd.Flags().GetString("dir")

	if profileFilter != "" {
		var filtered []session.Conversation
		for _, c := range convos {
			if c.Profile == profileFilter {
				filtered = append(filtered, c)
			}
		}
		convos = filtered
	}

	if dirFilter != "" {
		abs, _ := filepath.Abs(dirFilter)
		var filtered []session.Conversation
		for _, c := range convos {
			if strings.Contains(c.CWD, abs) || strings.Contains(c.CWD, dirFilter) {
				filtered = append(filtered, c)
			}
		}
		convos = filtered
	}

	out := cmd.OutOrStdout()

	if len(convos) == 0 {
		fmt.Fprintln(out, "No conversations found.")
		return nil
	}

	total := len(convos)
	if limit > 0 && len(convos) > limit {
		convos = convos[:limit]
	}

	fmt.Fprintf(out, "Found %d conversations (showing %d)\n\n", total, len(convos))

	for i := range convos {
		session.ScanFirstPrompt(&convos[i])
	}

	for _, c := range convos {
		age := shortAge(c.ModTime)
		sizeStr := humanSize(c.Size)
		prompt := c.FirstPrompt
		if prompt == "" {
			prompt = "(no prompt)"
		}
		if len(prompt) > 70 {
			prompt = prompt[:70] + "..."
		}

		profileColor := "\033[36m"
		dimColor := "\033[90m"
		reset := "\033[0m"
		if NoColor() {
			profileColor = ""
			dimColor = ""
			reset = ""
		}

		shortCwd := shortPath(c.CWD)

		fmt.Fprintf(out, "  %s%-8s%s %s%-6s%s %s%5s%s %3dm  %s%s%s  %s\n",
			profileColor, c.Profile, reset,
			dimColor, age, reset,
			dimColor, sizeStr, reset,
			c.Messages,
			dimColor, shortCwd, reset,
			prompt,
		)
	}

	if total > len(convos) {
		fmt.Fprintf(out, "\n  ... %d more. Use --limit to see more.\n", total-len(convos))
	}

	return nil
}

// ── dirs ──────────────────────────────────────────────────────────────

func newSessionsDirsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "dirs",
		Short: "List directories where Claude was used, with conversation counts.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSessionsDirs(cmd)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
}

func runSessionsDirs(cmd *cobra.Command) error {
	ui.Init(NoColor())
	claudeDir, isolateDir := sessionsBaseDirs()

	convos, err := session.ScanConversations(claudeDir, isolateDir)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if len(convos) == 0 {
		fmt.Fprintln(out, "No conversations found.")
		return nil
	}

	type dirInfo struct {
		count    int
		profiles map[string]bool
		latest   time.Time
		totalMsg int
	}
	dirs := make(map[string]*dirInfo)

	for _, c := range convos {
		d, ok := dirs[c.CWD]
		if !ok {
			d = &dirInfo{profiles: make(map[string]bool)}
			dirs[c.CWD] = d
		}
		d.count++
		d.profiles[c.Profile] = true
		d.totalMsg += c.Messages
		if c.ModTime.After(d.latest) {
			d.latest = c.ModTime
		}
	}

	type dirEntry struct {
		cwd  string
		info *dirInfo
	}
	var entries []dirEntry
	for cwd, info := range dirs {
		entries = append(entries, dirEntry{cwd, info})
	}
	// Sort by conversation count descending
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[j].info.count > entries[i].info.count {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}

	fmt.Fprintf(out, "Directories with Claude conversations (%d total across %d dirs)\n\n", len(convos), len(dirs))

	for _, e := range entries {
		var profs []string
		for p := range e.info.profiles {
			profs = append(profs, p)
		}

		dimColor := "\033[90m"
		cyanColor := "\033[36m"
		reset := "\033[0m"
		if NoColor() {
			dimColor = ""
			cyanColor = ""
			reset = ""
		}

		fmt.Fprintf(out, "  %s%-35s%s %3d convos  %4d msgs  %s%-6s%s  %s[%s]%s\n",
			cyanColor, shortPath(e.cwd), reset,
			e.info.count,
			e.info.totalMsg,
			dimColor, shortAge(e.info.latest), reset,
			dimColor, strings.Join(profs, ", "), reset,
		)
	}

	return nil
}

// ── show ──────────────────────────────────────────────────────────────

func newSessionsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <session-id>",
		Short: "Show details of a specific conversation.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSessionsShow(cmd, args[0])
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
}

func runSessionsShow(cmd *cobra.Command, id string) error {
	claudeDir, isolateDir := sessionsBaseDirs()
	convos, err := session.ScanConversations(claudeDir, isolateDir)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()

	for i, c := range convos {
		if c.SessionID == id || strings.HasPrefix(c.SessionID, id) {
			session.ScanFirstPrompt(&convos[i])
			c = convos[i]
			fmt.Fprintf(out, "Session:  %s\n", c.SessionID)
			fmt.Fprintf(out, "Profile:  %s\n", c.Profile)
			fmt.Fprintf(out, "CWD:      %s\n", c.CWD)
			fmt.Fprintf(out, "Messages: %d\n", c.Messages)
			fmt.Fprintf(out, "Size:     %s\n", humanSize(c.Size))
			fmt.Fprintf(out, "Modified: %s (%s)\n", c.ModTime.Format("2006-01-02 15:04"), shortAge(c.ModTime))
			fmt.Fprintf(out, "File:     %s\n", c.FilePath)
			fmt.Fprintf(out, "\nFirst prompt: %s\n", c.FirstPrompt)
			fmt.Fprintf(out, "\nResume with:\n  cd %s && claude --resume %s\n", c.CWD, c.SessionID)
			return nil
		}
	}

	return fmt.Errorf("no conversation found matching %q", id)
}

// ── clone ─────────────────────────────────────────────────────────

func newSessionsCloneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clone <session-id>",
		Short: "Clone a conversation to branch off in a new direction.",
		Long: `Copies a conversation's history to a new session ID so you can
resume the clone and take the conversation in a different direction
while keeping the original intact.`,
		Example: `  corch sessions clone abc123
  corch sessions clone abc123 && claude --resume <new-id>`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSessionsClone(cmd, args[0])
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
}

func runSessionsClone(cmd *cobra.Command, id string) error {
	claudeDir, isolateDir := sessionsBaseDirs()
	convos, err := session.ScanConversations(claudeDir, isolateDir)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()

	for i, c := range convos {
		if c.SessionID == id || strings.HasPrefix(c.SessionID, id) {
			session.ScanFirstPrompt(&convos[i])
			c = convos[i]

			clone, err := session.CloneConversation(&c)
			if err != nil {
				return fmt.Errorf("clone failed: %w", err)
			}

			fmt.Fprintf(out, "Cloned session:\n")
			fmt.Fprintf(out, "  Original: %s\n", c.SessionID)
			fmt.Fprintf(out, "  Clone:    %s\n", clone.SessionID)
			fmt.Fprintf(out, "  CWD:      %s\n", c.CWD)
			fmt.Fprintf(out, "  Profile:  %s\n", c.Profile)
			fmt.Fprintf(out, "  Size:     %s\n", humanSize(clone.Size))
			fmt.Fprintf(out, "\nResume clone with:\n  cd %s && claude --resume %s\n", c.CWD, clone.SessionID)
			return nil
		}
	}

	return fmt.Errorf("no conversation found matching %q", id)
}

// ── helpers ───────────────────────────────────────────────────────────

func shortAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dmo", int(d.Hours()/(24*30)))
	}
}

func humanSize(bytes int64) string {
	switch {
	case bytes < 1024:
		return fmt.Sprintf("%dB", bytes)
	case bytes < 1024*1024:
		return fmt.Sprintf("%.1fK", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%.1fM", float64(bytes)/(1024*1024))
	}
}

func shortPath(p string) string {
	home, _ := os.UserHomeDir()
	if strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}
