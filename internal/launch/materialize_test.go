package launch

import (
	"fmt"
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

// oauthBlob builds a minimal OAuth credential blob expiring at the given time.
func oauthBlob(access, expires string) string {
	return `{"claudeAiOauth":{"accessToken":"` + access +
		`","refreshToken":"r","expiresAt":"` + expires + `"}}`
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A second launch of a profile (e.g. from another folder) must not replace
// the tokens a running session already rotated in the isolate.
func TestMaterialize_KeepsFresherIsolateCredentials(t *testing.T) {
	profileDir, isolateDir, claudeHome := t.TempDir(), t.TempDir(), t.TempDir()
	writeTestFile(t, filepath.Join(profileDir, "credentials.json"), oauthBlob("stale", "2030-01-01T00:00:00Z"))
	writeTestFile(t, filepath.Join(isolateDir, ".credentials.json"), oauthBlob("fresh", "2031-01-01T00:00:00Z"))

	if _, err := Materialize(profileDir, isolateDir, claudeHome, false); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(isolateDir, ".credentials.json")); got != oauthBlob("fresh", "2031-01-01T00:00:00Z") {
		t.Errorf("isolate credentials were overwritten: %s", got)
	}
}

// Equal expiry is a tie, and ties keep what the isolate (the running
// session) already has.
func TestMaterialize_TieKeepsIsolateCredentials(t *testing.T) {
	profileDir, isolateDir, claudeHome := t.TempDir(), t.TempDir(), t.TempDir()
	writeTestFile(t, filepath.Join(profileDir, "credentials.json"), oauthBlob("p", "2030-01-01T00:00:00Z"))
	writeTestFile(t, filepath.Join(isolateDir, ".credentials.json"), oauthBlob("i", "2030-01-01T00:00:00Z"))

	if _, err := Materialize(profileDir, isolateDir, claudeHome, false); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(isolateDir, ".credentials.json")); got != oauthBlob("i", "2030-01-01T00:00:00Z") {
		t.Errorf("tie should keep isolate credentials, got: %s", got)
	}
}

// When the profile holds strictly fresher tokens the isolate is refreshed,
// and .claude.json is re-seeded alongside (identity may have changed).
func TestMaterialize_RefreshesWhenProfileFresher(t *testing.T) {
	profileDir, isolateDir, claudeHome := t.TempDir(), t.TempDir(), t.TempDir()
	writeTestFile(t, filepath.Join(profileDir, "credentials.json"), oauthBlob("new", "2031-01-01T00:00:00Z"))
	writeTestFile(t, filepath.Join(profileDir, "claude.json"), `{"from":"profile"}`)
	writeTestFile(t, filepath.Join(isolateDir, ".credentials.json"), oauthBlob("old", "2030-01-01T00:00:00Z"))
	writeTestFile(t, filepath.Join(isolateDir, ".claude.json"), `{"from":"isolate"}`)

	if _, err := Materialize(profileDir, isolateDir, claudeHome, false); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(isolateDir, ".credentials.json")); got != oauthBlob("new", "2031-01-01T00:00:00Z") {
		t.Errorf("credentials not refreshed: %s", got)
	}
	if got := readFile(t, filepath.Join(isolateDir, ".claude.json")); got != `{"from":"profile"}` {
		t.Errorf(".claude.json not re-seeded after refresh: %s", got)
	}
}

// .claude.json is session state once claude runs; it must survive a
// relaunch that does not replace the credentials.
func TestMaterialize_PreservesClaudeJSON(t *testing.T) {
	profileDir, isolateDir, claudeHome := t.TempDir(), t.TempDir(), t.TempDir()
	writeTestFile(t, filepath.Join(profileDir, "credentials.json"), oauthBlob("a", "2030-01-01T00:00:00Z"))
	writeTestFile(t, filepath.Join(profileDir, "claude.json"), `{"from":"profile"}`)
	writeTestFile(t, filepath.Join(isolateDir, ".credentials.json"), oauthBlob("a", "2030-01-01T00:00:00Z"))
	writeTestFile(t, filepath.Join(isolateDir, ".claude.json"), `{"projects":"live state"}`)

	if _, err := Materialize(profileDir, isolateDir, claudeHome, false); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(isolateDir, ".claude.json")); got != `{"projects":"live state"}` {
		t.Errorf(".claude.json was overwritten: %s", got)
	}
}

func TestMaterialize_SeedsEmptyIsolate(t *testing.T) {
	profileDir, isolateDir, claudeHome := t.TempDir(), t.TempDir(), t.TempDir()
	writeTestFile(t, filepath.Join(profileDir, "credentials.json"), oauthBlob("a", "2030-01-01T00:00:00Z"))
	writeTestFile(t, filepath.Join(profileDir, "claude.json"), `{"from":"profile"}`)

	if _, err := Materialize(profileDir, isolateDir, claudeHome, false); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(isolateDir, ".credentials.json")); got != oauthBlob("a", "2030-01-01T00:00:00Z") {
		t.Errorf("credentials not seeded: %s", got)
	}
	if got := readFile(t, filepath.Join(isolateDir, ".claude.json")); got != `{"from":"profile"}` {
		t.Errorf(".claude.json not seeded: %s", got)
	}
}

// A corrupt profile copy must never replace a usable isolate copy; a corrupt
// isolate copy must be replaced.
func TestProfileIsFresher_Unusable(t *testing.T) {
	good := []byte(oauthBlob("g", "2030-01-01T00:00:00Z"))
	bad := []byte(`not json`)
	if profileIsFresher(bad, good) {
		t.Error("corrupt profile copy must not replace a good isolate copy")
	}
	if !profileIsFresher(good, bad) {
		t.Error("good profile copy should replace a corrupt isolate copy")
	}
}

// On macOS Claude Code deletes .credentials.json once the login is in the
// Keychain. A live session with .claude.json but no credentials file owns the
// isolate; a joining launch must not write the (possibly stale) profile copy
// or reseed .claude.json over it.
func TestMaterialize_RunningSessionWithoutCredsFileIsLeftAlone(t *testing.T) {
	profileDir, isolateDir, claudeHome := t.TempDir(), t.TempDir(), t.TempDir()
	writeTestFile(t, filepath.Join(profileDir, "credentials.json"), oauthBlob("stale", "2030-01-01T00:00:00Z"))
	writeTestFile(t, filepath.Join(profileDir, "claude.json"), `{"from":"profile"}`)
	writeTestFile(t, filepath.Join(isolateDir, ".claude.json"), `{"projects":"live state"}`)
	// A live session (this test process) is registered in the isolate.
	writeTestFile(t, filepath.Join(isolateDir, "sessions", "1.json"), fmt.Sprintf(`{"pid":%d}`, os.Getpid()))

	if _, err := Materialize(profileDir, isolateDir, claudeHome, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(isolateDir, ".credentials.json")); err == nil {
		t.Error("profile credentials were written into a running session's isolate")
	}
	if got := readFile(t, filepath.Join(isolateDir, ".claude.json")); got != `{"projects":"live state"}` {
		t.Errorf(".claude.json was overwritten: %s", got)
	}
}

// Without a live session the same layout is just a leftover: seed it as usual.
func TestMaterialize_StaleIsolateWithoutCredsFileIsSeeded(t *testing.T) {
	profileDir, isolateDir, claudeHome := t.TempDir(), t.TempDir(), t.TempDir()
	writeTestFile(t, filepath.Join(profileDir, "credentials.json"), oauthBlob("a", "2030-01-01T00:00:00Z"))
	writeTestFile(t, filepath.Join(profileDir, "claude.json"), `{"from":"profile"}`)
	writeTestFile(t, filepath.Join(isolateDir, ".claude.json"), `{"old":"state"}`)
	writeTestFile(t, filepath.Join(isolateDir, "sessions", "1.json"), `{"pid":2147483646}`)

	if _, err := Materialize(profileDir, isolateDir, claudeHome, false); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(isolateDir, ".credentials.json")); got != oauthBlob("a", "2030-01-01T00:00:00Z") {
		t.Errorf("credentials not seeded: %s", got)
	}
}
