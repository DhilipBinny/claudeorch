package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupTestEnv(t *testing.T) (configHome string, sourceDir string, targetDir string) {
	t.Helper()
	configHome = t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configHome)

	sourceDir = "/test/source/project"
	targetDir = "/test/target/project"

	sourceSlug, _ := PathToSlug(sourceDir)
	sourceProject := filepath.Join(configHome, "projects", sourceSlug)
	os.MkdirAll(sourceProject, 0o755)

	return configHome, sourceDir, targetDir
}

func TestExecute_FullMigration(t *testing.T) {
	configHome, sourceDir, targetDir := setupTestEnv(t)

	sourceSlug, _ := PathToSlug(sourceDir)
	sourceProject := filepath.Join(configHome, "projects", sourceSlug)

	sid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

	seedIndex(t, sourceProject, IndexEntry{
		SessionID:   sid,
		Summary:     "test session",
		FirstPrompt: "hello",
		Modified:    "2026-05-01T00:00:00Z",
		Created:     "2026-05-01T00:00:00Z",
		ProjectPath: sourceDir,
	})

	jsonlLines := []string{
		`{"type":"user","message":{"content":"hello"},"cwd":"` + sourceDir + `"}`,
		`{"type":"assistant","message":{"content":"hi"},"cwd":"` + sourceDir + `"}`,
		`{"type":"user","message":{"content":"bye"},"cwd":"/other/dir"}`,
	}
	seedJSONL(t, sourceProject, sid, jsonlLines...)

	sessionSubDir := filepath.Join(sourceProject, sid)
	os.MkdirAll(sessionSubDir, 0o755)
	os.WriteFile(filepath.Join(sessionSubDir, "data.txt"), []byte("session data"), 0o644)

	historyDir := filepath.Join(configHome)
	historyLines := []string{
		`{"sessionId":"` + sid + `","project":"` + sourceDir + `","type":"start"}`,
		`{"sessionId":"other-session","project":"/other","type":"start"}`,
		`{"sessionId":"` + sid + `","project":"` + sourceDir + `","type":"end"}`,
	}
	os.WriteFile(filepath.Join(historyDir, "history.jsonl"), []byte(strings.Join(historyLines, "\n")+"\n"), 0o644)

	os.MkdirAll(filepath.Join(configHome, "backups"), 0o755)

	result, err := Execute(MigrateOptions{
		SourceDir: sourceDir,
		TargetDir: targetDir,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.SessionID != sid {
		t.Errorf("SessionID = %q, want %q", result.SessionID, sid)
	}
	if result.BackupDir == "" {
		t.Error("BackupDir is empty")
	}
	if result.CwdRewrites != 2 {
		t.Errorf("CwdRewrites = %d, want 2", result.CwdRewrites)
	}

	// Verify source JSONL removed
	if _, err := os.Stat(filepath.Join(sourceProject, sid+".jsonl")); !os.IsNotExist(err) {
		t.Error("source JSONL should be removed")
	}

	// Verify target JSONL exists with rewritten cwd
	targetSlug, _ := PathToSlug(targetDir)
	targetProject := filepath.Join(configHome, "projects", targetSlug)
	targetJSONL := filepath.Join(targetProject, sid+".jsonl")
	data, err := os.ReadFile(targetJSONL)
	if err != nil {
		t.Fatalf("read target JSONL: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("target JSONL lines = %d, want 3", len(lines))
	}
	var obj map[string]any
	json.Unmarshal([]byte(lines[0]), &obj)
	if obj["cwd"] != targetDir {
		t.Errorf("line 0 cwd = %v, want %q", obj["cwd"], targetDir)
	}
	json.Unmarshal([]byte(lines[2]), &obj)
	if obj["cwd"] != "/other/dir" {
		t.Errorf("line 2 cwd = %v, should be unchanged", obj["cwd"])
	}

	// Verify session subdir moved
	if _, err := os.Stat(filepath.Join(sourceProject, sid)); !os.IsNotExist(err) {
		t.Error("source session subdir should be removed")
	}
	movedData, err := os.ReadFile(filepath.Join(targetProject, sid, "data.txt"))
	if err != nil {
		t.Fatalf("read moved session data: %v", err)
	}
	if string(movedData) != "session data" {
		t.Errorf("moved data = %q", movedData)
	}

	// Verify source index updated
	srcIdx, _ := LoadSessionsIndex(sourceProject)
	if len(srcIdx.Entries) != 0 {
		t.Errorf("source index entries = %d, want 0", len(srcIdx.Entries))
	}

	// Verify target index updated
	tgtIdx, _ := LoadSessionsIndex(targetProject)
	if len(tgtIdx.Entries) != 1 {
		t.Fatalf("target index entries = %d, want 1", len(tgtIdx.Entries))
	}
	if tgtIdx.Entries[0].SessionID != sid {
		t.Errorf("target entry sessionId = %q", tgtIdx.Entries[0].SessionID)
	}
	if tgtIdx.Entries[0].ProjectPath != targetDir {
		t.Errorf("target entry projectPath = %q, want %q", tgtIdx.Entries[0].ProjectPath, targetDir)
	}

	// Verify history.jsonl updated
	histData, _ := os.ReadFile(filepath.Join(configHome, "history.jsonl"))
	histLines := strings.Split(strings.TrimSpace(string(histData)), "\n")
	rewrote := 0
	for _, line := range histLines {
		var h map[string]any
		json.Unmarshal([]byte(line), &h)
		if h["sessionId"] == sid && h["project"] == targetDir {
			rewrote++
		}
	}
	if rewrote != 2 {
		t.Errorf("history rewrites = %d, want 2", rewrote)
	}

	// Verify backup was created
	backupEntries, _ := os.ReadDir(result.BackupDir)
	if len(backupEntries) == 0 {
		t.Error("backup dir is empty")
	}
}

func TestExecute_DryRunDoesNotMutate(t *testing.T) {
	configHome, sourceDir, targetDir := setupTestEnv(t)

	sourceSlug, _ := PathToSlug(sourceDir)
	sourceProject := filepath.Join(configHome, "projects", sourceSlug)

	sid := "dry-run-test-session-id"
	seedIndex(t, sourceProject, IndexEntry{
		SessionID: sid,
		Modified:  "2026-05-01T00:00:00Z",
	})
	seedJSONL(t, sourceProject, sid, `{"type":"user","message":{"content":"test"},"cwd":"`+sourceDir+`"}`)

	actions, session, err := Plan(MigrateOptions{
		SourceDir: sourceDir,
		TargetDir: targetDir,
		DryRun:    true,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if session.SessionID != sid {
		t.Errorf("session = %q", session.SessionID)
	}
	if len(actions) == 0 {
		t.Error("no actions planned")
	}

	// Verify nothing changed
	if _, err := os.Stat(filepath.Join(sourceProject, sid+".jsonl")); os.IsNotExist(err) {
		t.Error("source JSONL should still exist after dry run")
	}
	idx, _ := LoadSessionsIndex(sourceProject)
	if len(idx.Entries) != 1 {
		t.Errorf("source index should be unchanged, entries = %d", len(idx.Entries))
	}
}

func TestExecute_RefusesRunningSession(t *testing.T) {
	configHome, sourceDir, targetDir := setupTestEnv(t)

	sourceSlug, _ := PathToSlug(sourceDir)
	sourceProject := filepath.Join(configHome, "projects", sourceSlug)

	sid := "running-session-id"
	seedIndex(t, sourceProject, IndexEntry{
		SessionID: sid,
		Modified:  "2026-05-01T00:00:00Z",
	})
	seedJSONL(t, sourceProject, sid, `{"type":"user","message":{"content":"test"}}`)

	// Create a fake PID tracker with our own PID (guaranteed alive)
	sessionsDir := filepath.Join(configHome, "sessions")
	os.MkdirAll(sessionsDir, 0o755)
	tracker, _ := json.Marshal(map[string]any{
		"sessionId": sid,
		"pid":       os.Getpid(),
	})
	os.WriteFile(filepath.Join(sessionsDir, "test.json"), tracker, 0o644)

	_, err := Execute(MigrateOptions{
		SourceDir: sourceDir,
		TargetDir: targetDir,
	})
	if err == nil {
		t.Fatal("expected error for running session")
	}
	if !strings.Contains(err.Error(), "currently running") {
		t.Errorf("error = %q, want 'currently running'", err.Error())
	}
}

func TestExecute_SessionNotFound(t *testing.T) {
	configHome, sourceDir, targetDir := setupTestEnv(t)

	sourceSlug, _ := PathToSlug(sourceDir)
	sourceProject := filepath.Join(configHome, "projects", sourceSlug)

	seedIndex(t, sourceProject, IndexEntry{
		SessionID: "existing-session",
		Modified:  "2026-05-01T00:00:00Z",
	})

	_, err := Execute(MigrateOptions{
		SourceDir: sourceDir,
		TargetDir: targetDir,
		SessionID: "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want 'not found'", err.Error())
	}
}

func TestExecute_PrefixMatch(t *testing.T) {
	configHome, sourceDir, targetDir := setupTestEnv(t)

	sourceSlug, _ := PathToSlug(sourceDir)
	sourceProject := filepath.Join(configHome, "projects", sourceSlug)

	sid := "abcdef12-3456-7890-abcd-ef1234567890"
	seedIndex(t, sourceProject, IndexEntry{
		SessionID: sid,
		Modified:  "2026-05-01T00:00:00Z",
	})
	seedJSONL(t, sourceProject, sid, `{"type":"user","message":{"content":"test"}}`)

	os.MkdirAll(filepath.Join(configHome, "backups"), 0o755)

	result, err := Execute(MigrateOptions{
		SourceDir: sourceDir,
		TargetDir: targetDir,
		SessionID: "abcdef12",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.SessionID != sid {
		t.Errorf("SessionID = %q, want %q", result.SessionID, sid)
	}
}

func TestExecute_NoSourceProject(t *testing.T) {
	_, _, targetDir := setupTestEnv(t)

	_, err := Execute(MigrateOptions{
		SourceDir: "/nonexistent/project/dir",
		TargetDir: targetDir,
	})
	if err == nil {
		t.Fatal("expected error for nonexistent source")
	}
}

func TestMoveJSONLWithRewrite(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "src")
	dstDir := filepath.Join(dir, "dst")
	os.MkdirAll(srcDir, 0o755)
	os.MkdirAll(dstDir, 0o755)

	oldCwd := "/old/project"
	newCwd := "/new/project"

	lines := []string{
		`{"type":"user","cwd":"` + oldCwd + `","message":"hello"}`,
		`{"type":"assistant","cwd":"` + oldCwd + `","message":"hi"}`,
		`{"type":"user","cwd":"/other","message":"bye"}`,
		`not valid json line`,
	}
	srcPath := filepath.Join(srcDir, "test.jsonl")
	os.WriteFile(srcPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644)

	dstPath := filepath.Join(dstDir, "test.jsonl")
	rewrites, err := moveJSONLWithRewrite(srcPath, dstPath, oldCwd, newCwd)
	if err != nil {
		t.Fatalf("moveJSONLWithRewrite: %v", err)
	}
	if rewrites != 2 {
		t.Errorf("rewrites = %d, want 2", rewrites)
	}

	// Source should be removed
	if _, err := os.Stat(srcPath); !os.IsNotExist(err) {
		t.Error("source should be removed")
	}

	// Check rewritten content
	data, _ := os.ReadFile(dstPath)
	resultLines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(resultLines) != 4 {
		t.Fatalf("result lines = %d, want 4", len(resultLines))
	}

	var obj map[string]any
	json.Unmarshal([]byte(resultLines[0]), &obj)
	if obj["cwd"] != newCwd {
		t.Errorf("line 0 cwd = %v, want %q", obj["cwd"], newCwd)
	}

	json.Unmarshal([]byte(resultLines[2]), &obj)
	if obj["cwd"] != "/other" {
		t.Errorf("line 2 cwd should be unchanged: %v", obj["cwd"])
	}

	// Invalid JSON should pass through
	if resultLines[3] != "not valid json line" {
		t.Errorf("invalid JSON line mangled: %q", resultLines[3])
	}
}
