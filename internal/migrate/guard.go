package migrate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DhilipBinny/claudeorch/internal/session"
)

// IsSessionRunning checks whether a session with the given ID is currently
// active by scanning PID tracker files in <configHome>/sessions/*.json.
// Returns the PID if running, 0 if not.
func IsSessionRunning(configHome, sessionID string) (bool, int, error) {
	sessionsDir := filepath.Join(configHome, "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, 0, nil
		}
		return false, 0, fmt.Errorf("migrate: read sessions dir: %w", err)
	}

	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(sessionsDir, e.Name()))
		if err != nil {
			continue
		}
		var tracker struct {
			SessionID string `json:"sessionId"`
			PID       int    `json:"pid"`
		}
		if json.Unmarshal(data, &tracker) != nil {
			continue
		}
		if tracker.SessionID == sessionID && tracker.PID > 0 && session.IsAlive(tracker.PID) {
			return true, tracker.PID, nil
		}
	}
	return false, 0, nil
}
