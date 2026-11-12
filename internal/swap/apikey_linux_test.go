//go:build linux

package swap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// These drive runKeychainSwap directly. Its credential helpers fall back to
// the flat file under test (the Secret Service is disabled in test binaries),
// so the routing and the "clear the other credential" behaviour can be
// checked without a real keychain. CLAUDE_CONFIG_DIR points them at a temp dir.

func readKeys(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(readFile(t, path)), &m); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return m
}

func swapFixture(t *testing.T, profileCreds, liveCreds string) (profileDir, claudeHome, claudeJSON string) {
	t.Helper()
	orch := t.TempDir()
	profileDir = filepath.Join(orch, "profiles", "p")
	claudeHome = t.TempDir()
	claudeJSON = filepath.Join(t.TempDir(), ".claude.json")
	t.Setenv("CLAUDE_CONFIG_DIR", claudeHome)

	writeFile(t, filepath.Join(profileDir, "credentials.json"), profileCreds)
	writeFile(t, filepath.Join(profileDir, "claude.json"), `{"from":"profile"}`)
	writeFile(t, filepath.Join(claudeHome, ".credentials.json"), liveCreds)
	writeFile(t, claudeJSON, `{"from":"old-live"}`)
	return
}

// Swapping to an API-key profile must route to the API-key path, write the
// key, and remove the stale OAuth tokens so Claude Code doesn't ignore the key.
func TestRunKeychainSwap_APIKeyProfile_ClearsOAuth(t *testing.T) {
	profileDir, claudeHome, claudeJSON := swapFixture(t,
		`{"apiKey":"sk-ant-test123"}`,
		`{"claudeAiOauth":{"accessToken":"old","refreshToken":"old","expiresAt":"2030-01-01T00:00:00Z"}}`)

	if err := runKeychainSwap(profileDir, claudeHome, claudeJSON); err != nil {
		t.Fatalf("runKeychainSwap: %v", err)
	}

	live := filepath.Join(claudeHome, ".credentials.json")
	if _, err := os.Stat(live); err != nil {
		t.Fatalf("live credentials file missing after swap: %v", err)
	}
	keys := readKeys(t, live)
	if _, ok := keys["apiKey"]; !ok {
		t.Errorf("API key not written: %v", keys)
	}
	if _, ok := keys["claudeAiOauth"]; ok {
		t.Errorf("stale OAuth tokens were not cleared: %v", keys)
	}
	if got := readFile(t, claudeJSON); got != `{"from":"profile"}` {
		t.Errorf(".claude.json = %s, want the profile's", got)
	}
}

// And the reverse: swapping to an OAuth profile removes a stale API key.
func TestRunKeychainSwap_OAuthProfile_ClearsAPIKey(t *testing.T) {
	profileDir, claudeHome, claudeJSON := swapFixture(t,
		`{"claudeAiOauth":{"accessToken":"new","refreshToken":"new","expiresAt":"2030-01-01T00:00:00Z"}}`,
		`{"apiKey":"sk-ant-test123"}`)

	if err := runKeychainSwap(profileDir, claudeHome, claudeJSON); err != nil {
		t.Fatalf("runKeychainSwap: %v", err)
	}

	keys := readKeys(t, filepath.Join(claudeHome, ".credentials.json"))
	if _, ok := keys["claudeAiOauth"]; !ok {
		t.Errorf("OAuth tokens not written: %v", keys)
	}
	if _, ok := keys["apiKey"]; ok {
		t.Errorf("stale API key was not cleared: %v", keys)
	}
}
