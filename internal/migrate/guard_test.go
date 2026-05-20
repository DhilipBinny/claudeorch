package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestIsSessionRunning_NotRunning(t *testing.T) {
	configHome := t.TempDir()
	sessionsDir := filepath.Join(configHome, "sessions")
	os.MkdirAll(sessionsDir, 0o755)

	tracker, _ := json.Marshal(map[string]any{
		"sessionId": "test-session",
		"pid":       999999999, // almost certainly not running
	})
	os.WriteFile(filepath.Join(sessionsDir, "test.json"), tracker, 0o644)

	running, pid, err := IsSessionRunning(configHome, "test-session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if running {
		t.Errorf("expected not running, got pid=%d", pid)
	}
}

func TestIsSessionRunning_Running(t *testing.T) {
	configHome := t.TempDir()
	sessionsDir := filepath.Join(configHome, "sessions")
	os.MkdirAll(sessionsDir, 0o755)

	tracker, _ := json.Marshal(map[string]any{
		"sessionId": "live-session",
		"pid":       os.Getpid(), // our own process — guaranteed alive
	})
	os.WriteFile(filepath.Join(sessionsDir, "test.json"), tracker, 0o644)

	running, pid, err := IsSessionRunning(configHome, "live-session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !running {
		t.Error("expected running")
	}
	if pid != os.Getpid() {
		t.Errorf("pid = %d, want %d", pid, os.Getpid())
	}
}

func TestIsSessionRunning_DifferentSession(t *testing.T) {
	configHome := t.TempDir()
	sessionsDir := filepath.Join(configHome, "sessions")
	os.MkdirAll(sessionsDir, 0o755)

	tracker, _ := json.Marshal(map[string]any{
		"sessionId": "other-session",
		"pid":       os.Getpid(),
	})
	os.WriteFile(filepath.Join(sessionsDir, "test.json"), tracker, 0o644)

	running, _, err := IsSessionRunning(configHome, "my-session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if running {
		t.Error("should not match different session ID")
	}
}

func TestIsSessionRunning_NoSessionsDir(t *testing.T) {
	configHome := t.TempDir()

	running, _, err := IsSessionRunning(configHome, "any-session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if running {
		t.Error("should not be running with no sessions dir")
	}
}

func TestIsSessionRunning_MalformedTracker(t *testing.T) {
	configHome := t.TempDir()
	sessionsDir := filepath.Join(configHome, "sessions")
	os.MkdirAll(sessionsDir, 0o755)

	os.WriteFile(filepath.Join(sessionsDir, "bad.json"), []byte("{invalid json"), 0o644)

	running, _, err := IsSessionRunning(configHome, "any-session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if running {
		t.Error("malformed tracker should not match")
	}
}
