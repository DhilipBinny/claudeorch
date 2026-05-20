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
	if !strings.Contains(err.Error(), "no session matching") {
		t.Errorf("error = %q, want 'no session matching'", err.Error())
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

func TestExecute_SameSourceTarget(t *testing.T) {
	_, sourceDir, _ := setupTestEnv(t)

	_, err := Execute(MigrateOptions{
		SourceDir: sourceDir,
		TargetDir: sourceDir,
	})
	if err == nil {
		t.Fatal("expected error for same source and target")
	}
	if !strings.Contains(err.Error(), "same") {
		t.Errorf("error = %q, want 'same'", err.Error())
	}
}

func TestPlan_RejectsPathTraversalSessionID(t *testing.T) {
	_, sourceDir, targetDir := setupTestEnv(t)

	configHome, _ := os.LookupEnv("CLAUDE_CONFIG_DIR")
	sourceSlug, _ := PathToSlug(sourceDir)
	sourceProject := filepath.Join(configHome, "projects", sourceSlug)

	maliciousSID := "../../../etc/cron.d/evil"
	seedIndex(t, sourceProject, IndexEntry{
		SessionID: maliciousSID,
		Modified:  "2026-05-01T00:00:00Z",
	})

	_, _, err := Plan(MigrateOptions{
		SourceDir: sourceDir,
		TargetDir: targetDir,
		SessionID: maliciousSID,
	})
	if err == nil {
		t.Fatal("expected error for path-traversal session ID")
	}
	if !strings.Contains(err.Error(), "invalid session ID") {
		t.Errorf("error = %q, want 'invalid session ID'", err.Error())
	}
}

func TestPlan_RejectsShortSessionID(t *testing.T) {
	_, sourceDir, targetDir := setupTestEnv(t)

	configHome, _ := os.LookupEnv("CLAUDE_CONFIG_DIR")
	sourceSlug, _ := PathToSlug(sourceDir)
	sourceProject := filepath.Join(configHome, "projects", sourceSlug)

	seedIndex(t, sourceProject, IndexEntry{
		SessionID: "short",
		Modified:  "2026-05-01T00:00:00Z",
	})

	_, _, err := Plan(MigrateOptions{
		SourceDir: sourceDir,
		TargetDir: targetDir,
		SessionID: "short",
	})
	if err == nil {
		t.Fatal("expected error for short session ID")
	}
	if !strings.Contains(err.Error(), "invalid session ID") {
		t.Errorf("error = %q, want 'invalid session ID'", err.Error())
	}
}

func TestIsValidSessionID(t *testing.T) {
	tests := []struct {
		sid  string
		want bool
	}{
		{"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", true},
		{"abcdef12-3456-7890-abcd-ef1234567890", true},
		{"short", false},
		{"", false},
		{"../../../etc/passwd", false},
		{"abc/def/ghi", false},
		{"abc\\def", false},
		{"valid-session-id-ok", true},
		{"has\x00null", false},
	}
	for _, tt := range tests {
		got := isValidSessionID(tt.sid)
		if got != tt.want {
			t.Errorf("isValidSessionID(%q) = %v, want %v", tt.sid, got, tt.want)
		}
	}
}

func TestSafePrefix(t *testing.T) {
	if safePrefix("abcdef", 3) != "abc" {
		t.Error("should truncate")
	}
	if safePrefix("ab", 5) != "ab" {
		t.Error("should return full string when shorter than n")
	}
	if safePrefix("", 3) != "" {
		t.Error("should handle empty")
	}
}

func TestMoveJSONLWithRewrite_PreservesUnchangedLines(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "src")
	dstDir := filepath.Join(dir, "dst")
	os.MkdirAll(srcDir, 0o755)
	os.MkdirAll(dstDir, 0o755)

	original := `{"type":"user","cwd":"/other","message":"hello","count":12345678901234}`
	srcPath := filepath.Join(srcDir, "test.jsonl")
	os.WriteFile(srcPath, []byte(original+"\n"), 0o644)

	dstPath := filepath.Join(dstDir, "test.jsonl")
	_, err := moveJSONLWithRewrite(srcPath, dstPath, "/old/dir", "/new/dir")
	if err != nil {
		t.Fatalf("moveJSONLWithRewrite: %v", err)
	}

	data, _ := os.ReadFile(dstPath)
	got := strings.TrimSpace(string(data))
	if got != original {
		t.Errorf("unchanged line was modified:\n  got:  %s\n  want: %s", got, original)
	}
}

func TestCopyDirAndRemove(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	os.MkdirAll(filepath.Join(src, "sub"), 0o755)
	os.WriteFile(filepath.Join(src, "file.txt"), []byte("data"), 0o644)
	os.WriteFile(filepath.Join(src, "sub", "nested.txt"), []byte("nested"), 0o644)

	if err := copyDirAndRemove(src, dst); err != nil {
		t.Fatalf("copyDirAndRemove: %v", err)
	}

	// Source must be removed
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("source directory should be removed after copy")
	}

	// Destination must have all files
	data, err := os.ReadFile(filepath.Join(dst, "file.txt"))
	if err != nil {
		t.Fatalf("read file.txt: %v", err)
	}
	if string(data) != "data" {
		t.Errorf("file.txt = %q, want %q", data, "data")
	}
	nested, err := os.ReadFile(filepath.Join(dst, "sub", "nested.txt"))
	if err != nil {
		t.Fatalf("read nested.txt: %v", err)
	}
	if string(nested) != "nested" {
		t.Errorf("nested.txt = %q, want %q", nested, "nested")
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

func TestMoveJSONLWithRewrite_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "empty.jsonl")
	os.WriteFile(srcPath, []byte{}, 0o644)

	dstPath := filepath.Join(dir, "dst.jsonl")
	rewrites, err := moveJSONLWithRewrite(srcPath, dstPath, "/old", "/new")
	if err != nil {
		t.Fatalf("moveJSONLWithRewrite: %v", err)
	}
	if rewrites != 0 {
		t.Errorf("rewrites = %d, want 0", rewrites)
	}
	if _, err := os.Stat(srcPath); !os.IsNotExist(err) {
		t.Error("source should be removed even if empty")
	}
}

func TestMoveJSONLWithRewrite_NonexistentSource(t *testing.T) {
	dir := t.TempDir()
	_, err := moveJSONLWithRewrite(filepath.Join(dir, "nope.jsonl"), filepath.Join(dir, "dst.jsonl"), "/old", "/new")
	if err == nil {
		t.Fatal("expected error for nonexistent source")
	}
}

func TestBackupFile_InvalidBackupName(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "data.txt")
	os.WriteFile(src, []byte("test"), 0o644)

	err := BackupFile(dir, src, "../escape.bak")
	if err == nil {
		t.Fatal("expected error for path traversal in backup name")
	}
	if !strings.Contains(err.Error(), "invalid backup name") {
		t.Errorf("error = %q", err.Error())
	}

	err = BackupFile(dir, src, "sub/dir.bak")
	if err == nil {
		t.Fatal("expected error for slash in backup name")
	}
}

func TestBackupFile_Permissions(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "data.txt")
	os.WriteFile(src, []byte("sensitive data"), 0o644)

	if err := BackupFile(dir, src, "data.txt.bak"); err != nil {
		t.Fatalf("BackupFile: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, "data.txt.bak"))
	if err != nil {
		t.Fatalf("stat backup: %v", err)
	}
	perm := info.Mode().Perm()
	if perm&0o077 != 0 {
		t.Errorf("backup file permissions = %04o, should not be group/world accessible", perm)
	}
}

func TestRewriteHistory_NoHistoryFile(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configHome)

	updated, err := rewriteHistory("/old/dir", "/new/dir", "some-session-id")
	if err != nil {
		t.Fatalf("rewriteHistory: %v", err)
	}
	if updated != 0 {
		t.Errorf("updated = %d, want 0", updated)
	}
}

func TestRewriteHistory_MalformedAndEmptyLines(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configHome)

	sid := "test-session-id-12345"
	lines := []string{
		`{"sessionId":"` + sid + `","project":"/old/dir","type":"start"}`,
		``,
		`not valid json`,
		`{"sessionId":"other","project":"/old/dir","type":"end"}`,
		`{"sessionId":"` + sid + `","project":"/old/dir","type":"end"}`,
	}
	os.WriteFile(filepath.Join(configHome, "history.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644)

	updated, err := rewriteHistory("/old/dir", "/new/dir", sid)
	if err != nil {
		t.Fatalf("rewriteHistory: %v", err)
	}
	if updated != 2 {
		t.Errorf("updated = %d, want 2", updated)
	}

	data, _ := os.ReadFile(filepath.Join(configHome, "history.jsonl"))
	result := strings.Split(string(data), "\n")

	// Empty line preserved
	if result[1] != "" {
		t.Errorf("empty line not preserved: %q", result[1])
	}
	// Malformed line preserved
	if result[2] != "not valid json" {
		t.Errorf("malformed line not preserved: %q", result[2])
	}
	// Unmatched session preserved
	var obj map[string]any
	json.Unmarshal([]byte(result[3]), &obj)
	if obj["project"] != "/old/dir" {
		t.Errorf("unmatched session should keep old dir: %v", obj["project"])
	}
}

func TestExecute_TargetAlreadyHasSession(t *testing.T) {
	configHome, sourceDir, targetDir := setupTestEnv(t)

	sourceSlug, _ := PathToSlug(sourceDir)
	sourceProject := filepath.Join(configHome, "projects", sourceSlug)

	sid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	seedIndex(t, sourceProject, IndexEntry{
		SessionID: sid,
		Summary:   "original session",
		Modified:  "2026-05-01T00:00:00Z",
	})
	seedJSONL(t, sourceProject, sid, `{"type":"user","message":{"content":"hello"},"cwd":"`+sourceDir+`"}`)

	// Pre-create target with an existing entry for the same session ID
	targetSlug, _ := PathToSlug(targetDir)
	targetProject := filepath.Join(configHome, "projects", targetSlug)
	seedIndex(t, targetProject, IndexEntry{
		SessionID: sid,
		Summary:   "stale target entry",
		Modified:  "2025-01-01T00:00:00Z",
	})

	historyDir := filepath.Join(configHome)
	os.WriteFile(filepath.Join(historyDir, "history.jsonl"), []byte{}, 0o644)
	os.MkdirAll(filepath.Join(configHome, "backups"), 0o755)

	result, err := Execute(MigrateOptions{
		SourceDir: sourceDir,
		TargetDir: targetDir,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.SessionID != sid {
		t.Errorf("SessionID = %q", result.SessionID)
	}

	// Target index should have exactly 1 entry (deduped, not 2)
	tgtIdx, _ := LoadSessionsIndex(targetProject)
	if len(tgtIdx.Entries) != 1 {
		t.Fatalf("target index entries = %d, want 1 (should dedup)", len(tgtIdx.Entries))
	}
	if tgtIdx.Entries[0].Summary != "original session" {
		t.Errorf("target entry summary = %q, want %q", tgtIdx.Entries[0].Summary, "original session")
	}
}

func TestExecute_BackupDirPermissions(t *testing.T) {
	configHome, sourceDir, targetDir := setupTestEnv(t)

	sourceSlug, _ := PathToSlug(sourceDir)
	sourceProject := filepath.Join(configHome, "projects", sourceSlug)

	sid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	seedIndex(t, sourceProject, IndexEntry{
		SessionID: sid,
		Modified:  "2026-05-01T00:00:00Z",
	})
	seedJSONL(t, sourceProject, sid, `{"type":"user","message":{"content":"test"}}`)

	os.MkdirAll(filepath.Join(configHome, "backups"), 0o755)
	os.WriteFile(filepath.Join(configHome, "history.jsonl"), []byte{}, 0o644)

	result, err := Execute(MigrateOptions{
		SourceDir: sourceDir,
		TargetDir: targetDir,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	info, err := os.Stat(result.BackupDir)
	if err != nil {
		t.Fatalf("stat backup dir: %v", err)
	}
	perm := info.Mode().Perm()
	if perm&0o077 != 0 {
		t.Errorf("backup dir permissions = %04o, should be 0700", perm)
	}
}

func TestExecute_TargetDirPermissions(t *testing.T) {
	configHome, sourceDir, targetDir := setupTestEnv(t)

	sourceSlug, _ := PathToSlug(sourceDir)
	sourceProject := filepath.Join(configHome, "projects", sourceSlug)

	sid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	seedIndex(t, sourceProject, IndexEntry{
		SessionID: sid,
		Modified:  "2026-05-01T00:00:00Z",
	})
	seedJSONL(t, sourceProject, sid, `{"type":"user","message":{"content":"test"}}`)

	os.MkdirAll(filepath.Join(configHome, "backups"), 0o755)
	os.WriteFile(filepath.Join(configHome, "history.jsonl"), []byte{}, 0o644)

	_, err := Execute(MigrateOptions{
		SourceDir: sourceDir,
		TargetDir: targetDir,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	targetSlug, _ := PathToSlug(targetDir)
	targetProject := filepath.Join(configHome, "projects", targetSlug)
	info, err := os.Stat(targetProject)
	if err != nil {
		t.Fatalf("stat target project: %v", err)
	}
	perm := info.Mode().Perm()
	if perm&0o077 != 0 {
		t.Errorf("target project dir permissions = %04o, should be 0700", perm)
	}
}
