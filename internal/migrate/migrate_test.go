package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPathToSlug(t *testing.T) {
	tests := []struct {
		name    string
		dir     string
		want    string
		wantErr bool
	}{
		{"standard path", "/home/binny/Desktop", "-home-binny-Desktop", false},
		{"root path", "/", "-", true},
		{"empty string", "", "", true},
		{"relative path", "relative/path", "", true},
		{"single dir", "/tmp", "-tmp", false},
		{"deep nesting", "/a/b/c/d/e", "-a-b-c-d-e", false},
		{"trailing slash", "/home/binny/", "-home-binny-", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := PathToSlug(tt.dir)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("PathToSlug(%q) expected error, got %q", tt.dir, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("PathToSlug(%q) unexpected error: %v", tt.dir, err)
			}
			if got != tt.want {
				t.Errorf("PathToSlug(%q) = %q, want %q", tt.dir, got, tt.want)
			}
		})
	}
}

func TestLoadSessionsIndex_Missing(t *testing.T) {
	dir := t.TempDir()
	idx, err := LoadSessionsIndex(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if idx.Version != 1 {
		t.Errorf("version = %d, want 1", idx.Version)
	}
	if len(idx.Entries) != 0 {
		t.Errorf("entries = %d, want 0", len(idx.Entries))
	}
}

func TestLoadSessionsIndex_Valid(t *testing.T) {
	dir := t.TempDir()
	idx := SessionsIndex{
		Version:      1,
		OriginalPath: "/test",
		Entries: []IndexEntry{
			{SessionID: "abc-123", Summary: "test session", Created: "2026-01-01T00:00:00Z"},
		},
	}
	data, _ := json.MarshalIndent(idx, "", "  ")
	os.WriteFile(filepath.Join(dir, "sessions-index.json"), data, 0o644)

	loaded, err := LoadSessionsIndex(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(loaded.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(loaded.Entries))
	}
	if loaded.Entries[0].SessionID != "abc-123" {
		t.Errorf("sessionId = %q, want %q", loaded.Entries[0].SessionID, "abc-123")
	}
}

func TestLoadSessionsIndex_Malformed(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "sessions-index.json"), []byte("{invalid"), 0o644)

	_, err := LoadSessionsIndex(dir)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestSaveSessionsIndex(t *testing.T) {
	dir := t.TempDir()
	idx := &SessionsIndex{
		Version:      1,
		OriginalPath: "/test",
		Entries: []IndexEntry{
			{SessionID: "abc-123", Summary: "saved session"},
		},
	}

	if err := SaveSessionsIndex(dir, idx); err != nil {
		t.Fatalf("SaveSessionsIndex: %v", err)
	}

	loaded, err := LoadSessionsIndex(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(loaded.Entries) != 1 || loaded.Entries[0].SessionID != "abc-123" {
		t.Errorf("round-trip failed: got %+v", loaded)
	}
}

func TestDiscoverSessions_Empty(t *testing.T) {
	dir := t.TempDir()
	sessions, err := DiscoverSessions(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("sessions = %d, want 0", len(sessions))
	}
}

func TestDiscoverSessions_IndexOnly(t *testing.T) {
	dir := t.TempDir()
	idx := SessionsIndex{
		Version: 1,
		Entries: []IndexEntry{
			{SessionID: "sid-1", Summary: "from index", Modified: "2026-01-01T00:00:00Z"},
		},
	}
	data, _ := json.MarshalIndent(idx, "", "  ")
	os.WriteFile(filepath.Join(dir, "sessions-index.json"), data, 0o644)

	sessions, err := DiscoverSessions(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(sessions))
	}
	if sessions[0].Source != "index" {
		t.Errorf("source = %q, want %q", sessions[0].Source, "index")
	}
	if sessions[0].HasJSONL {
		t.Error("HasJSONL should be false")
	}
}

func TestDiscoverSessions_JSONLOnly(t *testing.T) {
	dir := t.TempDir()
	jsonl := `{"type":"user","message":{"content":"hello world"}}` + "\n"
	os.WriteFile(filepath.Join(dir, "test-session-id.jsonl"), []byte(jsonl), 0o644)

	sessions, err := DiscoverSessions(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(sessions))
	}
	if sessions[0].SessionID != "test-session-id" {
		t.Errorf("sessionId = %q, want %q", sessions[0].SessionID, "test-session-id")
	}
	if sessions[0].Source != "filesystem" {
		t.Errorf("source = %q, want %q", sessions[0].Source, "filesystem")
	}
	if sessions[0].FirstPrompt != "hello world" {
		t.Errorf("firstPrompt = %q, want %q", sessions[0].FirstPrompt, "hello world")
	}
}

func TestDiscoverSessions_Merged(t *testing.T) {
	dir := t.TempDir()
	idx := SessionsIndex{
		Version: 1,
		Entries: []IndexEntry{
			{SessionID: "sid-1", Summary: "indexed", Modified: "2026-01-01T00:00:00Z"},
		},
	}
	data, _ := json.MarshalIndent(idx, "", "  ")
	os.WriteFile(filepath.Join(dir, "sessions-index.json"), data, 0o644)
	os.WriteFile(filepath.Join(dir, "sid-1.jsonl"), []byte(`{"type":"system"}`+"\n"), 0o644)

	sessions, err := DiscoverSessions(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(sessions))
	}
	if sessions[0].Source != "both" {
		t.Errorf("source = %q, want %q", sessions[0].Source, "both")
	}
	if !sessions[0].HasJSONL {
		t.Error("HasJSONL should be true")
	}
}

func TestDiscoverSessions_SortedByModified(t *testing.T) {
	dir := t.TempDir()
	idx := SessionsIndex{
		Version: 1,
		Entries: []IndexEntry{
			{SessionID: "old", Modified: "2025-01-01T00:00:00Z"},
			{SessionID: "new", Modified: "2026-06-01T00:00:00Z"},
			{SessionID: "mid", Modified: "2026-03-01T00:00:00Z"},
		},
	}
	data, _ := json.MarshalIndent(idx, "", "  ")
	os.WriteFile(filepath.Join(dir, "sessions-index.json"), data, 0o644)

	sessions, err := DiscoverSessions(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 3 {
		t.Fatalf("sessions = %d, want 3", len(sessions))
	}
	if sessions[0].SessionID != "new" {
		t.Errorf("first = %q, want %q", sessions[0].SessionID, "new")
	}
	if sessions[1].SessionID != "mid" {
		t.Errorf("second = %q, want %q", sessions[1].SessionID, "mid")
	}
	if sessions[2].SessionID != "old" {
		t.Errorf("third = %q, want %q", sessions[2].SessionID, "old")
	}
}

func TestExtractFirstPrompt_ContentArray(t *testing.T) {
	dir := t.TempDir()
	jsonl := `{"type":"system","message":{"content":"ignored"}}` + "\n" +
		`{"type":"user","message":{"content":[{"type":"text","text":"array prompt"}]}}` + "\n"
	path := filepath.Join(dir, "test.jsonl")
	os.WriteFile(path, []byte(jsonl), 0o644)

	got := extractFirstPrompt(path)
	if got != "array prompt" {
		t.Errorf("extractFirstPrompt = %q, want %q", got, "array prompt")
	}
}

func TestExtractFirstPrompt_Truncation(t *testing.T) {
	dir := t.TempDir()
	long := make([]byte, 200)
	for i := range long {
		long[i] = 'a'
	}
	jsonl := `{"type":"user","message":{"content":"` + string(long) + `"}}` + "\n"
	path := filepath.Join(dir, "test.jsonl")
	os.WriteFile(path, []byte(jsonl), 0o644)

	got := extractFirstPrompt(path)
	if len(got) > 120 {
		t.Errorf("len = %d, want <= 120", len(got))
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input string
		n     int
		want  string
	}{
		{"short", 10, "short"},
		{"this is a very long string", 15, "this is a ve..."},
		{"has\nnewlines\nin it", 50, "has newlines in it"},
		{"  leading spaces  ", 50, "leading spaces"},
	}
	for _, tt := range tests {
		got := truncate(tt.input, tt.n)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.n, got, tt.want)
		}
	}
}

func TestResolveSession(t *testing.T) {
	sessions := []SessionInfo{
		{SessionID: "abc-111-aaa", Modified: "2026-06-01T00:00:00Z"},
		{SessionID: "abc-222-bbb", Modified: "2026-05-01T00:00:00Z"},
		{SessionID: "def-333-ccc", Modified: "2026-04-01T00:00:00Z"},
	}

	t.Run("empty ID returns most recent", func(t *testing.T) {
		s, err := ResolveSession(sessions, "")
		if err != nil {
			t.Fatal(err)
		}
		if s.SessionID != "abc-111-aaa" {
			t.Errorf("got %q, want most recent", s.SessionID)
		}
	})

	t.Run("exact match", func(t *testing.T) {
		s, err := ResolveSession(sessions, "def-333-ccc")
		if err != nil {
			t.Fatal(err)
		}
		if s.SessionID != "def-333-ccc" {
			t.Errorf("got %q", s.SessionID)
		}
	})

	t.Run("unique prefix", func(t *testing.T) {
		s, err := ResolveSession(sessions, "def")
		if err != nil {
			t.Fatal(err)
		}
		if s.SessionID != "def-333-ccc" {
			t.Errorf("got %q", s.SessionID)
		}
	})

	t.Run("ambiguous prefix", func(t *testing.T) {
		_, err := ResolveSession(sessions, "abc")
		if err == nil {
			t.Fatal("expected error for ambiguous prefix")
		}
	})

	t.Run("no match", func(t *testing.T) {
		_, err := ResolveSession(sessions, "zzz")
		if err == nil {
			t.Fatal("expected error for no match")
		}
	})

	t.Run("empty sessions", func(t *testing.T) {
		_, err := ResolveSession(nil, "")
		if err == nil {
			t.Fatal("expected error for empty sessions")
		}
	})
}

// helper to set CLAUDE_CONFIG_DIR for tests that call functions using paths.ClaudeConfigHome
func setTestConfigDir(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
}

func seedIndex(t *testing.T, dir string, entries ...IndexEntry) {
	t.Helper()
	idx := SessionsIndex{
		Version: 1,
		Entries: entries,
	}
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sessions-index.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func seedJSONL(t *testing.T, dir, sessionID string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	// Set a known mtime for deterministic sorting
	os.Chtimes(path, time.Now(), time.Now())
}
