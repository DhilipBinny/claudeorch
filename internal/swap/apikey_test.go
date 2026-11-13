package swap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func keysOf(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(readFile(t, path)), &m); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return m
}

const oauthLive = `"claudeAiOauth":{"accessToken":"old","refreshToken":"old","expiresAt":1893456000000}`

func apiKeyFixture(t *testing.T, profileClaude, liveCreds string) (profileDir, claudeHome, claudeJSON string) {
	t.Helper()
	orch := t.TempDir()
	profileDir = filepath.Join(orch, "profiles", "p")
	claudeHome = t.TempDir()
	claudeJSON = filepath.Join(t.TempDir(), ".claude.json")

	writeFile(t, filepath.Join(profileDir, "credentials.json"), `{"apiKey":"sk-ant-test123"}`)
	writeFile(t, filepath.Join(profileDir, "claude.json"), profileClaude)
	if liveCreds != "" {
		writeFile(t, filepath.Join(claudeHome, ".credentials.json"), liveCreds)
	}
	writeFile(t, claudeJSON, `{"from":"old-live"}`)
	return
}

// The key must land where Claude Code reads it (primaryApiKey in
// .claude.json), the OAuth login must go, and other top-level keys in
// .credentials.json (mcpOAuth) must survive.
func TestRunFileAPIKeySwap_WritesPrimaryApiKeyAndKeepsMCPTokens(t *testing.T) {
	profileDir, claudeHome, claudeJSON := apiKeyFixture(t,
		`{"oauthAccount":{"emailAddress":"a@x.com"},"theme":"dark"}`,
		`{`+oauthLive+`,"mcpOAuth":{"srv":{"token":"keep-me"}}}`)

	if err := runFileAPIKeySwap(profileDir, claudeHome, claudeJSON, "sk-ant-test123"); err != nil {
		t.Fatalf("runFileAPIKeySwap: %v", err)
	}

	cj := keysOf(t, claudeJSON)
	if string(cj["primaryApiKey"]) != `"sk-ant-test123"` {
		t.Errorf("primaryApiKey = %s", cj["primaryApiKey"])
	}
	if _, ok := cj["oauthAccount"]; !ok {
		t.Error("profile identity (oauthAccount) was not carried over")
	}
	if _, ok := cj["theme"]; !ok {
		t.Error("other .claude.json keys from the profile were dropped")
	}
	if _, ok := cj["from"]; ok {
		t.Error("old live .claude.json content leaked through")
	}

	creds := keysOf(t, filepath.Join(claudeHome, ".credentials.json"))
	if _, ok := creds["claudeAiOauth"]; ok {
		t.Error("OAuth login was not removed, Claude Code would prefer it")
	}
	if _, ok := creds["apiKey"]; ok {
		t.Error("an apiKey was written to .credentials.json, which Claude Code ignores")
	}
	if _, ok := creds["mcpOAuth"]; !ok {
		t.Error("mcpOAuth tokens were deleted")
	}
	for _, p := range []string{claudeJSON + ".pre-swap", filepath.Join(claudeHome, ".credentials.json.pre-swap")} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("backup %s was left behind", p)
		}
	}
}

// With only an OAuth login in the file, nothing is left to keep: the file goes.
func TestRunFileAPIKeySwap_RemovesCredentialsFileWhenNothingElseIsInIt(t *testing.T) {
	profileDir, claudeHome, claudeJSON := apiKeyFixture(t, `{"oauthAccount":{}}`, `{`+oauthLive+`}`)
	if err := runFileAPIKeySwap(profileDir, claudeHome, claudeJSON, "sk-ant-test123"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(claudeHome, ".credentials.json")); err == nil {
		t.Error("an empty credentials file was left behind")
	}
}

func TestRunFileAPIKeySwap_NoLiveCredentialsFile(t *testing.T) {
	profileDir, claudeHome, claudeJSON := apiKeyFixture(t, `{"oauthAccount":{}}`, "")
	if err := runFileAPIKeySwap(profileDir, claudeHome, claudeJSON, "sk-ant-test123"); err != nil {
		t.Fatal(err)
	}
	if string(keysOf(t, claudeJSON)["primaryApiKey"]) != `"sk-ant-test123"` {
		t.Error("primaryApiKey not written")
	}
}

// A stale primaryApiKey carried in the profile's claude.json is replaced.
func TestRunFileAPIKeySwap_ReplacesStalePrimaryApiKey(t *testing.T) {
	profileDir, claudeHome, claudeJSON := apiKeyFixture(t, `{"primaryApiKey":"sk-ant-OLD"}`, "")
	if err := runFileAPIKeySwap(profileDir, claudeHome, claudeJSON, "sk-ant-test123"); err != nil {
		t.Fatal(err)
	}
	if got := string(keysOf(t, claudeJSON)["primaryApiKey"]); got != `"sk-ant-test123"` {
		t.Errorf("primaryApiKey = %s", got)
	}
}

// A live credentials file we cannot parse must not be rewritten, and nothing
// else may change either.
func TestRunFileAPIKeySwap_UnparseableLiveCredentialsChangesNothing(t *testing.T) {
	profileDir, claudeHome, claudeJSON := apiKeyFixture(t, `{"oauthAccount":{}}`, `not json`)
	if err := runFileAPIKeySwap(profileDir, claudeHome, claudeJSON, "sk-ant-test123"); err == nil {
		t.Fatal("expected an error")
	}
	if got := readFile(t, filepath.Join(claudeHome, ".credentials.json")); got != "not json" {
		t.Errorf("live credentials were modified: %s", got)
	}
	if got := readFile(t, claudeJSON); got != `{"from":"old-live"}` {
		t.Errorf(".claude.json was modified: %s", got)
	}
}

// Writing .claude.json fails (the target path is a directory): the swap must
// undo exactly what it did and report the error.
func TestRunFileAPIKeySwap_RollsBackWhenClaudeJSONWriteFails(t *testing.T) {
	profileDir, claudeHome, claudeJSON := apiKeyFixture(t, `{"oauthAccount":{}}`,
		`{`+oauthLive+`,"mcpOAuth":{"s":{}}}`)
	origCreds := readFile(t, filepath.Join(claudeHome, ".credentials.json"))

	// Make the .claude.json destination unwritable: its parent is a file.
	blocker := filepath.Join(t.TempDir(), "blocker")
	writeFile(t, blocker, "x")
	badClaudeJSON := filepath.Join(blocker, ".claude.json")
	_ = claudeJSON

	if err := runFileAPIKeySwap(profileDir, claudeHome, badClaudeJSON, "sk-ant-test123"); err == nil {
		t.Fatal("expected an error")
	}
	if got := readFile(t, filepath.Join(claudeHome, ".credentials.json")); got != origCreds {
		t.Errorf("credentials not restored: %s", got)
	}
}

// Run routes by profile type, and an OAuth profile still takes the old path.
func TestRun_RoutesAPIKeyProfileToFileSwapOnLinux(t *testing.T) {
	if _, err := os.Stat("/proc"); err != nil {
		t.Skip("file mode only")
	}
	profileDir, claudeHome, claudeJSON := apiKeyFixture(t, `{"oauthAccount":{}}`, `{`+oauthLive+`}`)
	t.Setenv("CLAUDEORCH_NO_KEYCHAIN", "1")
	if err := Run(profileDir, t.TempDir(), claudeHome, claudeJSON); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if string(keysOf(t, claudeJSON)["primaryApiKey"]) != `"sk-ant-test123"` {
		t.Error("API-key profile did not get primaryApiKey")
	}
}
