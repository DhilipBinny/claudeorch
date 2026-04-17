package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/DhilipBinny/claudeorch/internal/fsio"
	"github.com/DhilipBinny/claudeorch/internal/paths"
	"github.com/DhilipBinny/claudeorch/internal/profile"
	"github.com/spf13/cobra"
)

func init() {
	registerSubcommand(func(root *cobra.Command) {
		root.AddCommand(newStatuslineCmd())
	})
}

func newStatuslineCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "statusline",
		Short: "Render Claude Code's statusline with a profile indicator.",
		Long: `Reads Claude Code's statusLine JSON from stdin and prints a colored
status line that includes the active claudeorch profile.

This is meant to be invoked by Claude itself — not run manually. Wire it
up once with 'claudeorch statusline install' and every future claude
session will show which profile it's using.

The profile is detected from:
  1. CLAUDE_CONFIG_DIR env var — matches an isolate dir → that profile
  2. The 'active' pointer in ~/.claudeorch/store.json
  3. Empty when neither is available

Designed to never fail the caller: on any error, prints a minimal fallback
line so Claude's UI still renders.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return renderStatusline(cmd.InOrStdin(), cmd.OutOrStdout())
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(newStatuslineInstallCmd())
	return cmd
}

// statuslineInput mirrors the subset of Claude's statusLine JSON we render.
// All fields are optional — fall back gracefully when any are missing or
// the schema changes upstream.
type statuslineInput struct {
	Workspace struct {
		CurrentDir string `json:"current_dir"`
	} `json:"workspace"`
	Cwd   string `json:"cwd"`
	Model struct {
		DisplayName string `json:"display_name"`
	} `json:"model"`
	ContextWindow struct {
		UsedPercentage float64 `json:"used_percentage"`
	} `json:"context_window"`
}

// renderStatusline is the hot path — runs on every Claude prompt refresh.
// Must never panic or exit non-zero; Claude's UI would go blank otherwise.
func renderStatusline(stdin io.Reader, stdout io.Writer) error {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintln(stdout, "claude")
		}
	}()

	data, readErr := io.ReadAll(io.LimitReader(stdin, 1<<20))
	if readErr != nil {
		fmt.Fprintln(stdout, "claude")
		return nil
	}

	var in statuslineInput
	_ = json.Unmarshal(data, &in) // ignore errors — fields stay zero-valued

	cwd := in.Workspace.CurrentDir
	if cwd == "" {
		cwd = in.Cwd
	}
	if cwd == "" {
		cwd = "?"
	}
	dir := filepath.Base(cwd)

	model := in.Model.DisplayName
	if model == "" {
		model = "Claude"
	}

	profileName := detectProfile()

	gitInfo := gitInfoFor(cwd)

	ctx := ""
	if in.ContextWindow.UsedPercentage > 0 {
		ctx = fmt.Sprintf(" \x1b[2m[ctx: %.0f%%]\x1b[0m", in.ContextWindow.UsedPercentage)
	}

	profileTag := ""
	if profileName != "" {
		profileTag = fmt.Sprintf("\x1b[1;36m[%s]\x1b[0m ", profileName)
	}

	gitSegment := ""
	if gitInfo != "" {
		gitSegment = " " + gitInfo
	}

	fmt.Fprintf(stdout, "\x1b[1;32m➜\x1b[0m 🕊  %s\x1b[0;36m%s\x1b[0m%s \x1b[2m%s\x1b[0m%s\n",
		profileTag, dir, gitSegment, model, ctx)
	return nil
}

// detectProfile returns the claudeorch profile name currently in use:
//   - If CLAUDE_CONFIG_DIR points inside ~/.claudeorch/isolate/<name>/ → <name>
//   - Else if store.json has an active pointer → active profile name
//   - Else "" (plain claude with no saved active profile)
func detectProfile() string {
	if envDir := os.Getenv("CLAUDE_CONFIG_DIR"); envDir != "" {
		if isolatesRoot, err := paths.IsolatesRoot(); err == nil {
			prefix := filepath.Clean(isolatesRoot) + string(filepath.Separator)
			cleaned := filepath.Clean(envDir)
			if strings.HasPrefix(cleaned, prefix) {
				rel := cleaned[len(prefix):]
				if idx := strings.IndexByte(rel, filepath.Separator); idx >= 0 {
					return rel[:idx]
				}
				return rel
			}
		}
	}
	storePath, err := paths.StoreFile()
	if err != nil {
		return ""
	}
	store, err := profile.Load(storePath)
	if err != nil || store.Active == nil {
		return ""
	}
	return *store.Active
}

// gitInfoFor returns a colored "git:(branch) ✗" fragment if cwd is inside
// a git repo, or "" if not (or if git isn't installed).
func gitInfoFor(cwd string) string {
	if cwd == "" || cwd == "?" {
		return ""
	}
	branchCmd := exec.Command("git", "-C", cwd, "rev-parse", "--abbrev-ref", "HEAD")
	out, err := branchCmd.Output()
	if err != nil {
		return ""
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" {
		return ""
	}
	dirtyCmd := exec.Command("git", "-C", cwd, "status", "--porcelain")
	statusOut, _ := dirtyCmd.Output()
	dirty := strings.TrimSpace(string(statusOut)) != ""
	if dirty {
		return fmt.Sprintf("\x1b[1;34mgit:(\x1b[0;31m%s\x1b[1;34m) \x1b[0;33m✗\x1b[0m", branch)
	}
	return fmt.Sprintf("\x1b[1;34mgit:(\x1b[0;31m%s\x1b[1;34m)\x1b[0m", branch)
}

// newStatuslineInstallCmd wires 'claudeorch statusline' into Claude's
// settings.json so users don't have to hand-edit the file.
func newStatuslineInstallCmd() *cobra.Command {
	var uninstall bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Wire 'claudeorch statusline' into ~/.claude/settings.json.",
		Long: `Writes or updates the 'statusLine' key in Claude Code's settings.json
to call 'claudeorch statusline' on each prompt refresh.

The existing settings.json is preserved — only the statusLine key is set.
Use --uninstall to remove it.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatuslineInstall(cmd, uninstall)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.Flags().BoolVar(&uninstall, "uninstall", false, "remove the claudeorch statusLine entry from settings.json")
	return cmd
}

func runStatuslineInstall(cmd *cobra.Command, uninstall bool) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve binary path: %w", err)
	}
	if resolved, evalErr := filepath.EvalSymlinks(exe); evalErr == nil {
		exe = resolved
	}

	claudeHome, err := paths.ClaudeConfigHome()
	if err != nil {
		return err
	}
	settingsPath := filepath.Join(claudeHome, "settings.json")

	settings := make(map[string]any)
	var existingMode os.FileMode = 0o600
	if info, statErr := os.Stat(settingsPath); statErr == nil {
		existingMode = info.Mode().Perm()
		data, readErr := os.ReadFile(settingsPath)
		if readErr != nil {
			return fmt.Errorf("read existing settings.json: %w", readErr)
		}
		if len(data) > 0 {
			if jsonErr := json.Unmarshal(data, &settings); jsonErr != nil {
				return fmt.Errorf("parse existing settings.json: %w", jsonErr)
			}
		}
	}

	desiredCmd := exe + " statusline"

	if uninstall {
		existing, ok := settings["statusLine"].(map[string]any)
		if !ok {
			fmt.Fprintln(cmd.OutOrStdout(), "No statusLine entry present — nothing to uninstall.")
			return nil
		}
		if curr, _ := existing["command"].(string); !strings.Contains(curr, "claudeorch statusline") {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"Warning: statusLine command is not claudeorch's (%q). Leaving it alone.\n", curr)
			return nil
		}
		delete(settings, "statusLine")
		if err := writeSettings(settingsPath, settings, existingMode); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Removed claudeorch statusLine from "+settingsPath)
		return nil
	}

	if existing, ok := settings["statusLine"].(map[string]any); ok {
		if curr, _ := existing["command"].(string); curr == desiredCmd {
			fmt.Fprintln(cmd.OutOrStdout(), "statusLine already configured for claudeorch — no change.")
			return nil
		}
		fmt.Fprintf(cmd.ErrOrStderr(),
			"Warning: overwriting existing statusLine command: %v\n", existing["command"])
	}

	settings["statusLine"] = map[string]any{
		"type":    "command",
		"command": desiredCmd,
	}

	if err := writeSettings(settingsPath, settings, existingMode); err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Configured %s\n", settingsPath)
	fmt.Fprintf(cmd.OutOrStdout(), "Command:   %s\n", desiredCmd)
	fmt.Fprintln(cmd.OutOrStdout(), "\nOpen a claude session to see the profile indicator in the statusline.")
	return nil
}

func writeSettings(path string, settings map[string]any, mode os.FileMode) error {
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}
	data = append(data, '\n')
	if err := fsio.WriteFileAtomic(path, data, mode); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	return nil
}
