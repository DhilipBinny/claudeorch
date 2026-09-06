// Package watch monitors claudeorch tmux sessions for idle/active state
// and sends OS notifications when a Claude instance needs attention.
package watch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DhilipBinny/claudeorch/internal/mux"
	"github.com/DhilipBinny/claudeorch/internal/notify"
)

// State tracks the observed state of a window.
type State int

const (
	StateUnknown State = iota
	StateActive
	StateIdle
)

func (s State) String() string {
	switch s {
	case StateActive:
		return "ACTIVE"
	case StateIdle:
		return "IDLE"
	default:
		return "UNKNOWN"
	}
}

// WindowState holds the tracked state for one tmux window.
type WindowState struct {
	Session    string    `json:"session"`
	Window     int       `json:"window"`
	Profile    string    `json:"profile"`
	State      State     `json:"state"`
	Since      time.Time `json:"since"`
	LastOutput string    `json:"-"`
	Notified   bool      `json:"-"`
}

// Snapshot captures the state of all windows at a point in time.
type Snapshot struct {
	Timestamp time.Time      `json:"timestamp"`
	Windows   []WindowState  `json:"windows"`
}

// DetectState examines tmux pane output to determine if Claude is idle.
// Claude Code shows a prompt character (❯ or >) when waiting for input.
func DetectState(sessionName string, windowIdx int) (State, string) {
	output, err := mux.CapturePaneOutput(sessionName, windowIdx, 20)
	if err != nil {
		return StateUnknown, ""
	}

	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")

	// Walk from the bottom to find the last non-empty line.
	var lastLine string
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed != "" {
			lastLine = trimmed
			break
		}
	}

	if lastLine == "" {
		return StateUnknown, output
	}

	if isIdlePrompt(lastLine) {
		return StateIdle, output
	}

	return StateActive, output
}

func isIdlePrompt(line string) bool {
	// Claude Code's input prompt variants.
	trimmed := strings.TrimSpace(line)

	// The main prompt: ❯ or >
	if trimmed == "❯" || trimmed == ">" || trimmed == "❯ " || trimmed == "> " {
		return true
	}
	// Prompt with mode indicators like "❯ " or ">>> "
	if strings.HasSuffix(trimmed, "❯") || strings.HasSuffix(trimmed, "❯ ") {
		return true
	}
	// The prompt might have ANSI codes stripped, look for common patterns.
	stripped := StripANSI(trimmed)
	stripped = strings.TrimSpace(stripped)
	if stripped == ">" || stripped == "❯" {
		return true
	}
	return false
}

// StripANSI removes ANSI escape sequences from a string.
func StripANSI(s string) string {
	var out strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && !((s[j] >= 'A' && s[j] <= 'Z') || (s[j] >= 'a' && s[j] <= 'z')) {
				j++
			}
			if j < len(s) {
				j++
			}
			i = j
			continue
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

// PollOnce scans all claudeorch tmux sessions and returns a snapshot.
func PollOnce() Snapshot {
	sessions, err := mux.ListSessions()
	if err != nil || len(sessions) == 0 {
		return Snapshot{Timestamp: time.Now()}
	}

	var states []WindowState
	for _, s := range sessions {
		for _, w := range s.Windows {
			state, _ := DetectState(s.Name, w.Index)
			states = append(states, WindowState{
				Session: s.Name,
				Window:  w.Index,
				Profile: s.Profile,
				State:   state,
				Since:   time.Now(),
			})
		}
	}
	return Snapshot{Timestamp: time.Now(), Windows: states}
}

// Watcher polls tmux sessions and sends notifications on state changes.
type Watcher struct {
	Interval time.Duration
	stop     chan struct{}
	states   map[string]*WindowState // "session:window" -> state
}

// New creates a new Watcher with the given poll interval.
func New(interval time.Duration) *Watcher {
	return &Watcher{
		Interval: interval,
		stop:     make(chan struct{}),
		states:   make(map[string]*WindowState),
	}
}

// Run starts the watcher loop. It blocks until Stop is called.
func (w *Watcher) Run() {
	ticker := time.NewTicker(w.Interval)
	defer ticker.Stop()

	// First poll immediately.
	w.poll()

	for {
		select {
		case <-ticker.C:
			w.poll()
		case <-w.stop:
			return
		}
	}
}

// Stop signals the watcher to stop.
func (w *Watcher) Stop() {
	close(w.stop)
}

func (w *Watcher) poll() {
	sessions, err := mux.ListSessions()
	if err != nil {
		return
	}

	seen := make(map[string]bool)

	for _, s := range sessions {
		for _, win := range s.Windows {
			key := fmt.Sprintf("%s:%d", s.Name, win.Index)
			seen[key] = true

			state, _ := DetectState(s.Name, win.Index)

			prev, exists := w.states[key]
			if !exists {
				w.states[key] = &WindowState{
					Session: s.Name,
					Window:  win.Index,
					Profile: s.Profile,
					State:   state,
					Since:   time.Now(),
				}
				continue
			}

			if state != prev.State {
				prev.State = state
				prev.Since = time.Now()
				prev.Notified = false
			}

			// Notify when newly idle and not yet notified.
			if state == StateIdle && !prev.Notified {
				idle := time.Since(prev.Since)
				if idle >= 5*time.Second {
					title := fmt.Sprintf("claudeorch: %s:%d", s.Name, win.Index)
					msg := fmt.Sprintf("[%s] Claude is waiting for input", s.Profile)
					_ = notify.Send(title, msg)
					prev.Notified = true
				}
			}
		}
	}

	// Clean up windows that no longer exist.
	for key := range w.states {
		if !seen[key] {
			delete(w.states, key)
		}
	}
}

// PidFilePath returns the path to the watcher PID file.
func PidFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claudeorch", "watcher.pid"), nil
}

// StatusFilePath returns the path to the watcher status file.
func StatusFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claudeorch", "watcher.json"), nil
}

// WriteStatus writes the current snapshot to the status file.
func WriteStatus(snap Snapshot) error {
	path, err := StatusFilePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// ReadStatus reads the last snapshot from the status file.
func ReadStatus() (*Snapshot, error) {
	path, err := StatusFilePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}
