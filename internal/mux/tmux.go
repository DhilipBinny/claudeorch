// Package mux wraps the tmux CLI for managing claudeorch multiplexer sessions.
//
// Sessions are prefixed with "co-" to avoid collisions with user tmux sessions.
// Each session stores its claudeorch profile name in a tmux environment variable
// (CLAUDEORCH_PROFILE) so windows inherit the profile automatically.
package mux

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	Prefix     = "co-"
	ProfileEnv = "CLAUDEORCH_PROFILE"
)

// ErrTmuxNotFound is returned when tmux is not installed.
var ErrTmuxNotFound = fmt.Errorf("tmux not found — install with: brew install tmux (macOS) or apt install tmux (Linux)")

// EnsureTmux checks that tmux is available on PATH.
func EnsureTmux() error {
	_, err := exec.LookPath("tmux")
	if err != nil {
		return ErrTmuxNotFound
	}
	return nil
}

// SessionName returns the tmux session name for a user-facing name.
func SessionName(name string) string {
	return Prefix + name
}

// UserName strips the prefix from a tmux session name.
func UserName(tmuxName string) string {
	return strings.TrimPrefix(tmuxName, Prefix)
}

// Session represents a running claudeorch tmux session.
type Session struct {
	Name     string // user-facing name (without prefix)
	Profile  string
	Windows  []Window
	Attached int
}

// Window represents a tmux window within a session.
type Window struct {
	Index int
	Name  string
	CWD   string
}

// SessionExists checks whether a tmux session exists.
func SessionExists(name string) bool {
	return exec.Command("tmux", "has-session", "-t", SessionName(name)).Run() == nil
}

// GetProfile reads the CLAUDEORCH_PROFILE env var from a tmux session.
func GetProfile(sessionName string) (string, error) {
	out, err := tmuxOutput("show-environment", "-t", SessionName(sessionName), ProfileEnv)
	if err != nil {
		return "", fmt.Errorf("no profile set for session %q", sessionName)
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.HasPrefix(line, ProfileEnv+"=") {
			return strings.TrimPrefix(line, ProfileEnv+"="), nil
		}
	}
	return "", fmt.Errorf("no profile set for session %q", sessionName)
}

// CreateSession creates a new tmux session with the first window.
func CreateSession(name, profile, windowName, shellCmd string) error {
	tmuxName := SessionName(name)
	if err := tmuxRun("new-session", "-d", "-s", tmuxName, "-n", windowName, shellCmd); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	if err := tmuxRun("set-environment", "-t", tmuxName, ProfileEnv, profile); err != nil {
		return fmt.Errorf("set profile env: %w", err)
	}
	return nil
}

// AddWindow adds a new window to an existing session.
func AddWindow(sessionName, windowName, shellCmd string) error {
	return tmuxRun("new-window", "-t", SessionName(sessionName), "-n", windowName, shellCmd)
}

// SelectWindow selects a window by index.
func SelectWindow(sessionName string, index int) error {
	return tmuxRun("select-window", "-t", fmt.Sprintf("%s:%d", SessionName(sessionName), index))
}

// ListSessions returns all claudeorch tmux sessions.
func ListSessions() ([]Session, error) {
	out, err := tmuxOutput("list-sessions", "-F", "#{session_name}\t#{session_attached}")
	if err != nil {
		return nil, nil // no tmux server running
	}

	var sessions []Session
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		name := parts[0]
		attached := 0
		if len(parts) > 1 {
			attached, _ = strconv.Atoi(parts[1])
		}
		if !strings.HasPrefix(name, Prefix) {
			continue
		}
		userName := UserName(name)
		profile, _ := GetProfile(userName)

		windows, _ := listWindows(name)
		sessions = append(sessions, Session{
			Name:     userName,
			Profile:  profile,
			Windows:  windows,
			Attached: attached,
		})
	}
	return sessions, nil
}

func listWindows(tmuxSession string) ([]Window, error) {
	out, err := tmuxOutput("list-windows", "-t", tmuxSession,
		"-F", "#{window_index}\t#{window_name}\t#{pane_current_path}")
	if err != nil {
		return nil, err
	}

	var windows []Window
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		idx, _ := strconv.Atoi(parts[0])
		windows = append(windows, Window{
			Index: idx,
			Name:  parts[1],
			CWD:   parts[2],
		})
	}
	return windows, nil
}

// SendKeys sends text to a specific window, followed by Enter.
func SendKeys(sessionName string, windowIndex int, text string) error {
	target := fmt.Sprintf("%s:%d", SessionName(sessionName), windowIndex)
	if err := tmuxRun("send-keys", "-t", target, "-l", text); err != nil {
		return err
	}
	return tmuxRun("send-keys", "-t", target, "Enter")
}

// CapturePaneOutput captures the visible pane content from a window.
func CapturePaneOutput(sessionName string, windowIndex int, lines int) (string, error) {
	target := fmt.Sprintf("%s:%d", SessionName(sessionName), windowIndex)
	return tmuxOutput("capture-pane", "-p", "-t", target, "-S", fmt.Sprintf("-%d", lines))
}

// Attach attaches to a session. This replaces the current process.
func Attach(sessionName string) error {
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		return ErrTmuxNotFound
	}
	return execSyscall(tmuxPath, []string{"tmux", "attach", "-t", SessionName(sessionName)})
}

// KillSession kills an entire tmux session.
func KillSession(sessionName string) error {
	return tmuxRun("kill-session", "-t", SessionName(sessionName))
}

// KillWindow kills a specific window in a session.
func KillWindow(sessionName string, windowIndex int) error {
	target := fmt.Sprintf("%s:%d", SessionName(sessionName), windowIndex)
	return tmuxRun("kill-window", "-t", target)
}

// WindowCount returns the number of windows in a session.
func WindowCount(sessionName string) (int, error) {
	out, err := tmuxOutput("list-windows", "-t", SessionName(sessionName), "-F", "#{window_index}")
	if err != nil {
		return 0, err
	}
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line != "" {
			count++
		}
	}
	return count, nil
}

// BuildLaunchCmd builds the shell command to launch claude via claudeorch in a tmux window.
func BuildLaunchCmd(exe, profile, cwd, extraArgs string) string {
	var parts []string
	if cwd != "" {
		abs, err := filepath.Abs(cwd)
		if err == nil {
			cwd = abs
		}
		parts = append(parts, fmt.Sprintf("cd %s", shellQuote(cwd)))
	}

	launchCmd := fmt.Sprintf("%s launch --force %s", shellQuote(exe), shellQuote(profile))
	if extraArgs != "" {
		launchCmd += " " + extraArgs
	}
	parts = append(parts, launchCmd)

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	parts = append(parts, fmt.Sprintf("exec %s", shell))

	return strings.Join(parts, " && ")
}

// IsTTY reports whether stdout is a terminal.
func IsTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func tmuxRun(args ...string) error {
	cmd := exec.Command("tmux", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func tmuxOutput(args ...string) (string, error) {
	cmd := exec.Command("tmux", args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '/' || c == '.' || c == '~') {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
