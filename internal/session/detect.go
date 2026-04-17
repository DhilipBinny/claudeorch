// Package session detects running Claude Code instances by reading PID files
// from the active config directory.
//
// Two sources are scanned:
//   - <configDir>/sessions/*.json  — terminal sessions
//   - <configDir>/ide/*.lock       — IDE (VS Code / JetBrains) sessions
//
// Dead PIDs are silently skipped. Only live PIDs are returned.
package session

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Session represents a running terminal Claude Code session.
type Session struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	CWD       string `json:"cwd"`
	StartedAt string `json:"startedAt"`
	Kind      string `json:"kind"`
}

// IDE represents a running Claude Code IDE extension session.
type IDE struct {
	PID       int      `json:"pid"`
	IDEName   string   `json:"ideName"`
	Workspace []string `json:"workspaceFolders"`
}

// Sessions returns all live Claude Code sessions (terminal + IDE) for the
// given configDir. Malformed or stale files are silently skipped.
func Sessions(configDir string) ([]Session, []IDE, error) {
	sessions, err := scanSessions(filepath.Join(configDir, "sessions"))
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	ides, err := scanIDEs(filepath.Join(configDir, "ide"))
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	return sessions, ides, nil
}

// HasLiveSession reports whether any live Claude Code session is detected.
func HasLiveSession(configDir string) (bool, error) {
	sessions, ides, err := Sessions(configDir)
	if err != nil {
		return false, err
	}
	return len(sessions) > 0 || len(ides) > 0, nil
}

func scanSessions(dir string) ([]Session, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var live []Session
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var s Session
		if err := json.Unmarshal(data, &s); err != nil || s.PID <= 0 {
			continue
		}
		if IsAlive(s.PID) {
			live = append(live, s)
		}
	}
	return live, nil
}

func scanIDEs(dir string) ([]IDE, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var live []IDE
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".lock" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var ide IDE
		if err := json.Unmarshal(data, &ide); err != nil || ide.PID <= 0 {
			continue
		}
		if IsAlive(ide.PID) {
			live = append(live, ide)
		}
	}
	return live, nil
}
