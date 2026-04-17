package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writePIDFile(t *testing.T, dir, name string, v any) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(v)
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestIsAlive_Self(t *testing.T) {
	if !IsAlive(os.Getpid()) {
		t.Error("IsAlive(self) = false, want true")
	}
}

func TestIsAlive_DeadPID(t *testing.T) {
	// PID 2^31-1 is guaranteed to not exist on Linux.
	const hugePID = 1<<31 - 1
	if IsAlive(hugePID) {
		t.Errorf("IsAlive(%d) = true, want false (process can't exist)", hugePID)
	}
}

func TestIsAlive_InvalidPID(t *testing.T) {
	// pid ≤ 0 must return false without reaching kill(2) — kill(0, 0)
	// signals the process group and kill(-1, 0) signals every process.
	if IsAlive(0) {
		t.Error("IsAlive(0) = true, want false (guarded)")
	}
	if IsAlive(-1) {
		t.Error("IsAlive(-1) = true, want false (guarded)")
	}
	if IsAlive(-9999) {
		t.Error("IsAlive(-9999) = true, want false (guarded)")
	}
}

func TestSessions_Empty(t *testing.T) {
	configDir := t.TempDir()
	sessions, ides, err := Sessions(configDir)
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(sessions) != 0 || len(ides) != 0 {
		t.Errorf("expected empty result on empty dirs, got sessions=%d ides=%d", len(sessions), len(ides))
	}
}

func TestSessions_LiveSession(t *testing.T) {
	configDir := t.TempDir()
	sessionsDir := filepath.Join(configDir, "sessions")

	writePIDFile(t, sessionsDir, "abc.json", map[string]any{
		"pid":       os.Getpid(),
		"sessionId": "abc",
		"cwd":       "/tmp",
		"kind":      "terminal",
	})

	sessions, _, err := Sessions(configDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Errorf("expected 1 live session, got %d", len(sessions))
	}
}

func TestSessions_DeadSessionSkipped(t *testing.T) {
	configDir := t.TempDir()
	sessionsDir := filepath.Join(configDir, "sessions")

	// Dead PID (won't exist).
	writePIDFile(t, sessionsDir, "dead.json", map[string]any{
		"pid":       1<<31 - 1,
		"sessionId": "dead",
		"cwd":       "/tmp",
	})

	sessions, _, err := Sessions(configDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 live sessions (dead PID), got %d", len(sessions))
	}
}

func TestSessions_MalformedJSONSkipped(t *testing.T) {
	configDir := t.TempDir()
	sessionsDir := filepath.Join(configDir, "sessions")

	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionsDir, "bad.json"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	sessions, _, err := Sessions(configDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions (malformed JSON skipped), got %d", len(sessions))
	}
}

func TestSessions_LiveIDE(t *testing.T) {
	configDir := t.TempDir()
	ideDir := filepath.Join(configDir, "ide")

	writePIDFile(t, ideDir, "vscode.lock", map[string]any{
		"pid":              os.Getpid(),
		"ideName":          "vscode",
		"workspaceFolders": []string{"/home/user/project"},
	})

	_, ides, err := Sessions(configDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ides) != 1 {
		t.Errorf("expected 1 live IDE session, got %d", len(ides))
	}
}

func TestHasLiveSession_True(t *testing.T) {
	configDir := t.TempDir()
	sessionsDir := filepath.Join(configDir, "sessions")
	writePIDFile(t, sessionsDir, "live.json", map[string]any{
		"pid":       os.Getpid(),
		"sessionId": "live",
	})
	alive, err := HasLiveSession(configDir)
	if err != nil {
		t.Fatal(err)
	}
	if !alive {
		t.Error("HasLiveSession = false, want true")
	}
}

func TestHasLiveSession_False(t *testing.T) {
	configDir := t.TempDir()
	alive, err := HasLiveSession(configDir)
	if err != nil {
		t.Fatal(err)
	}
	if alive {
		t.Error("HasLiveSession = true on empty dir, want false")
	}
}
