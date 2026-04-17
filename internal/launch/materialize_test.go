package launch

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMaterialize_Happy(t *testing.T) {
	profileDir := t.TempDir()
	isolateDir := t.TempDir()
	claudeHome := t.TempDir()

	writeTestFile(t, filepath.Join(profileDir, "credentials.json"),
		`{"claudeAiOauth":{"accessToken":"tok","refreshToken":"ref","expiresAt":"2030-01-01T00:00:00Z"}}`)
	writeTestFile(t, filepath.Join(profileDir, "claude.json"),
		`{"oauthAccount":{"emailAddress":"e@e.com","organizationUuid":"u"}}`)

	dir, err := Materialize(profileDir, isolateDir, claudeHome, false)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if dir != isolateDir {
		t.Errorf("returned dir = %s, want %s", dir, isolateDir)
	}

	// Credentials must exist at 0600.
	info, err := os.Stat(filepath.Join(isolateDir, ".credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("credentials mode = %04o, want 0600", info.Mode().Perm())
	}
}

func TestMaterialize_Idempotent(t *testing.T) {
	profileDir := t.TempDir()
	isolateDir := t.TempDir()
	claudeHome := t.TempDir()

	writeTestFile(t, filepath.Join(profileDir, "credentials.json"),
		`{"claudeAiOauth":{"accessToken":"tok","refreshToken":"ref","expiresAt":"2030-01-01T00:00:00Z"}}`)
	writeTestFile(t, filepath.Join(profileDir, "claude.json"),
		`{"oauthAccount":{"emailAddress":"e@e.com","organizationUuid":"u"}}`)

	// Call twice — second call must not error.
	if _, err := Materialize(profileDir, isolateDir, claudeHome, false); err != nil {
		t.Fatal(err)
	}
	if _, err := Materialize(profileDir, isolateDir, claudeHome, false); err != nil {
		t.Fatalf("second Materialize: %v", err)
	}
}

func TestMaterialize_SymlinkCreated(t *testing.T) {
	profileDir := t.TempDir()
	isolateDir := t.TempDir()
	claudeHome := t.TempDir()

	writeTestFile(t, filepath.Join(profileDir, "credentials.json"),
		`{"claudeAiOauth":{"accessToken":"tok","refreshToken":"ref","expiresAt":"2030-01-01T00:00:00Z"}}`)
	writeTestFile(t, filepath.Join(profileDir, "claude.json"),
		`{"oauthAccount":{"emailAddress":"e@e.com","organizationUuid":"u"}}`)

	// Create a CLAUDE.md in claude config home.
	writeTestFile(t, filepath.Join(claudeHome, "CLAUDE.md"), "# instructions")

	if _, err := Materialize(profileDir, isolateDir, claudeHome, false); err != nil {
		t.Fatal(err)
	}

	link, err := os.Readlink(filepath.Join(isolateDir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("CLAUDE.md symlink missing: %v", err)
	}
	if link != filepath.Join(claudeHome, "CLAUDE.md") {
		t.Errorf("symlink target = %q, want %q", link, filepath.Join(claudeHome, "CLAUDE.md"))
	}
}

func TestMaterialize_BrokenSymlinkRepaired(t *testing.T) {
	profileDir := t.TempDir()
	isolateDir := t.TempDir()
	claudeHome := t.TempDir()

	writeTestFile(t, filepath.Join(profileDir, "credentials.json"),
		`{"claudeAiOauth":{"accessToken":"tok","refreshToken":"ref","expiresAt":"2030-01-01T00:00:00Z"}}`)
	writeTestFile(t, filepath.Join(profileDir, "claude.json"),
		`{"oauthAccount":{"emailAddress":"e@e.com","organizationUuid":"u"}}`)

	// Create a broken symlink for CLAUDE.md.
	dst := filepath.Join(isolateDir, "CLAUDE.md")
	_ = os.Symlink("/nonexistent/CLAUDE.md", dst)

	// Create the real source.
	writeTestFile(t, filepath.Join(claudeHome, "CLAUDE.md"), "# real")

	if _, err := Materialize(profileDir, isolateDir, claudeHome, false); err != nil {
		t.Fatal(err)
	}

	link, err := os.Readlink(dst)
	if err != nil {
		t.Fatal(err)
	}
	if link != filepath.Join(claudeHome, "CLAUDE.md") {
		t.Errorf("symlink not repaired, still points to: %s", link)
	}
}

func TestMaterialize_IsolatedNoSymlinks(t *testing.T) {
	profileDir := t.TempDir()
	isolateDir := t.TempDir()
	claudeHome := t.TempDir()

	writeTestFile(t, filepath.Join(profileDir, "credentials.json"),
		`{"claudeAiOauth":{"accessToken":"tok","refreshToken":"ref","expiresAt":"2030-01-01T00:00:00Z"}}`)
	writeTestFile(t, filepath.Join(profileDir, "claude.json"),
		`{"oauthAccount":{"emailAddress":"e@e.com","organizationUuid":"u"}}`)
	writeTestFile(t, filepath.Join(claudeHome, "CLAUDE.md"), "# instructions")

	if _, err := Materialize(profileDir, isolateDir, claudeHome, true); err != nil {
		t.Fatal(err)
	}

	// In isolated mode, no symlinks should be created.
	if _, err := os.Lstat(filepath.Join(isolateDir, "CLAUDE.md")); err == nil {
		t.Error("CLAUDE.md symlink created in isolated mode")
	}
}
