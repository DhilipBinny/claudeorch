package swap

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// TestRun_Happy verifies the golden swap path: files arrive at live paths,
// backups are cleaned up, staging dir is removed.
func TestRun_Happy(t *testing.T) {
	orchHome := t.TempDir()
	profileDir := filepath.Join(orchHome, "profiles", "work")
	_ = os.MkdirAll(profileDir, 0o700)
	claudeHome := t.TempDir()
	homeDir := t.TempDir()
	claudeJSONPath := filepath.Join(homeDir, ".claude.json")

	// Set up profile files.
	writeFile(t, filepath.Join(profileDir, "credentials.json"), `{"claudeAiOauth":{"accessToken":"new_tok","refreshToken":"new_ref","expiresAt":"2030-01-01T00:00:00Z"}}`)
	writeFile(t, filepath.Join(profileDir, "claude.json"), `{"oauthAccount":{"emailAddress":"new@example.com","organizationUuid":"org-new"}}`)

	// Set up existing live files.
	writeFile(t, filepath.Join(claudeHome, ".credentials.json"), `{"claudeAiOauth":{"accessToken":"old_tok","refreshToken":"old_ref","expiresAt":"2020-01-01T00:00:00Z"}}`)
	writeFile(t, claudeJSONPath, `{"oauthAccount":{"emailAddress":"old@example.com","organizationUuid":"org-old"}}`)

	if err := Run(profileDir, orchHome, claudeHome, claudeJSONPath); err != nil {
		t.Fatalf("Run: %v", err)
	}

	credsContent := readFile(t, filepath.Join(claudeHome, ".credentials.json"))
	if credsContent != `{"claudeAiOauth":{"accessToken":"new_tok","refreshToken":"new_ref","expiresAt":"2030-01-01T00:00:00Z"}}` {
		t.Errorf("credentials.json has wrong content: %s", credsContent)
	}
	claudeContent := readFile(t, claudeJSONPath)
	if claudeContent != `{"oauthAccount":{"emailAddress":"new@example.com","organizationUuid":"org-new"}}` {
		t.Errorf(".claude.json has wrong content: %s", claudeContent)
	}

	// Backups must be cleaned up.
	if _, err := os.Stat(filepath.Join(claudeHome, ".credentials.json.pre-swap")); err == nil {
		t.Error(".pre-swap backup not removed after successful swap")
	}

	// No tmp-swap dirs should remain in orchHome.
	entries, _ := os.ReadDir(orchHome)
	for _, e := range entries {
		if e.IsDir() && len(e.Name()) > 9 && e.Name()[:9] == "tmp-swap-" {
			t.Errorf("staging dir not cleaned up: %s", e.Name())
		}
	}
}

// TestRun_NoExistingLiveFiles verifies swap works when target paths don't exist yet.
func TestRun_NoExistingLiveFiles(t *testing.T) {
	orchHome := t.TempDir()
	profileDir := filepath.Join(orchHome, "profiles", "work")
	_ = os.MkdirAll(profileDir, 0o700)
	claudeHome := t.TempDir()
	homeDir := t.TempDir()
	claudeJSONPath := filepath.Join(homeDir, ".claude.json")

	writeFile(t, filepath.Join(profileDir, "credentials.json"), `{"claudeAiOauth":{"accessToken":"tok","refreshToken":"ref","expiresAt":"2030-01-01T00:00:00Z"}}`)
	writeFile(t, filepath.Join(profileDir, "claude.json"), `{"oauthAccount":{"emailAddress":"e@e.com","organizationUuid":"u"}}`)

	if err := Run(profileDir, orchHome, claudeHome, claudeJSONPath); err != nil {
		t.Fatalf("Run on empty target: %v", err)
	}

	if _, err := os.Stat(filepath.Join(claudeHome, ".credentials.json")); err != nil {
		t.Error("credentials.json not created")
	}
}

// TestRun_ByteIdenticalRestore verifies that if Run encounters an error, the
// original content is byte-identical restored.
func TestRun_ByteIdenticalRestore(t *testing.T) {
	orchHome := t.TempDir()
	profileDir := filepath.Join(orchHome, "profiles", "work")
	_ = os.MkdirAll(profileDir, 0o700)
	claudeHome := t.TempDir()
	homeDir := t.TempDir()
	claudeJSONPath := filepath.Join(homeDir, ".claude.json")

	origCreds := `{"claudeAiOauth":{"accessToken":"original_tok","refreshToken":"original_ref","expiresAt":"2020-01-01T00:00:00Z"}}`
	origClaude := `{"oauthAccount":{"emailAddress":"original@example.com","organizationUuid":"org-orig"}}`

	writeFile(t, filepath.Join(profileDir, "credentials.json"), `{"claudeAiOauth":{"accessToken":"new_tok","refreshToken":"new_ref","expiresAt":"2030-01-01T00:00:00Z"}}`)
	// Intentionally provide a bad claude.json path that can't be written — by making
	// the target directory a file instead (making rename fail on CommitB).
	// Write a file at claudeJSONPath's parent dir location as a file, not dir.
	writeFile(t, filepath.Join(claudeHome, ".credentials.json"), origCreds)
	writeFile(t, claudeJSONPath, origClaude)

	// Make staging dir for the claude.json target unwritable to force CommitB failure.
	// We do this by using a path where the intermediate dir doesn't exist.
	badClaudeJSONPath := filepath.Join(homeDir, "nonexistent", ".claude.json")

	// This should fail at CommitB and roll back CommitA.
	err := Run(profileDir, orchHome, claudeHome, badClaudeJSONPath)
	if err == nil {
		t.Fatal("expected error when CommitB destination is unreachable")
	}

	// Credentials must be restored to original content.
	gotCreds := readFile(t, filepath.Join(claudeHome, ".credentials.json"))
	if gotCreds != origCreds {
		t.Errorf("credentials not restored after rollback\ngot: %s\nwant: %s", gotCreds, origCreds)
	}
}
