//go:build integration

package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const testAPIKey = "sk-ant-api03-synthetic-test-key"

// writeAPIKeyLogin sets up the live state of a Console (API-key) login: a
// primaryApiKey and identity in .claude.json, and no .credentials.json.
func (e *Env) writeAPIKeyLogin(email, orgUUID, key string) {
	e.t.Helper()
	_ = os.Remove(filepath.Join(e.ClaudeConfigDir, ".credentials.json"))
	e.writeJSON(filepath.Join(e.ClaudeConfigDir, ".claude.json"), map[string]any{
		"primaryApiKey": key,
		"oauthAccount": map[string]any{
			"emailAddress":     email,
			"organizationUuid": orgUUID,
			"organizationName": "Org",
		},
	}, 0o600)
}

func (e *Env) liveClaudeJSON() map[string]json.RawMessage {
	e.t.Helper()
	b, err := os.ReadFile(filepath.Join(e.ClaudeConfigDir, ".claude.json"))
	if err != nil {
		e.t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		e.t.Fatal(err)
	}
	return m
}

func skipIfKeychain(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "darwin" {
		t.Skip("API-key swap is not supported on macOS yet")
	}
}

func TestAPIKey_AddSwapRoundTripPreservesOtherCredentials(t *testing.T) {
	skipIfKeychain(t)
	env := NewEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "")

	// An OAuth account that also has MCP tokens in the credentials file.
	env.WriteClaudeJSON("alice@example.com", "org-1", "Acme")
	env.WriteCredentials("tok", "ref")
	creds := filepath.Join(env.ClaudeConfigDir, ".credentials.json")
	raw, _ := os.ReadFile(creds)
	var m map[string]json.RawMessage
	_ = json.Unmarshal(raw, &m)
	m["mcpOAuth"] = json.RawMessage(`{"srv":{"token":"keep-me"}}`)
	env.writeJSON(creds, m, 0o600)
	env.Run("add", "oauth").AssertSuccess(t)

	// A Console login, saved as an API-key profile.
	env.writeAPIKeyLogin("bob@example.com", "org-2", testAPIKey)
	r := env.Run("add", "console")
	r.AssertSuccess(t)
	if blob := env.ReadProfileCredentials("console"); !strings.Contains(string(blob), testAPIKey) {
		t.Fatalf("API key not saved in the profile: %s", blob)
	}

	// Back to OAuth, then onto the API-key profile.
	env.Run("swap", "oauth").AssertSuccess(t)
	env.Run("swap", "console").AssertSuccess(t)

	if got := string(env.liveClaudeJSON()["primaryApiKey"]); got != `"`+testAPIKey+`"` {
		t.Errorf("primaryApiKey after swap = %s", got)
	}
	after, err := os.ReadFile(creds)
	if err != nil {
		t.Fatalf("credentials file missing after swap: %v", err)
	}
	if strings.Contains(string(after), "claudeAiOauth") {
		t.Errorf("OAuth login still present, Claude Code would prefer it: %s", after)
	}
	if strings.Contains(string(after), "apiKey") {
		t.Errorf("apiKey written to .credentials.json, Claude Code ignores it: %s", after)
	}

	// And back: the OAuth profile must come back without the API key.
	env.Run("swap", "oauth").AssertSuccess(t)
	if _, ok := env.liveClaudeJSON()["primaryApiKey"]; ok {
		t.Error("primaryApiKey survived the swap back to OAuth")
	}
}

// 'add' for an account already saved must update its Source when the
// credential type changed.
func TestAPIKey_AddUpdatesSourceWhenTypeChanges(t *testing.T) {
	env := NewEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "")
	env.WriteClaudeJSON("alice@example.com", "org-1", "Acme")
	env.WriteCredentials("tok", "ref")
	env.Run("add", "acct").AssertSuccess(t)
	if b, _ := os.ReadFile(env.StoreFile()); !strings.Contains(string(b), `"oauth"`) {
		t.Fatalf("expected an oauth profile: %s", b)
	}

	env.writeAPIKeyLogin("alice@example.com", "org-1", testAPIKey)
	env.Run("add", "acct").AssertSuccess(t)
	if b, _ := os.ReadFile(env.StoreFile()); !strings.Contains(string(b), `"apikey"`) {
		t.Errorf("Source not updated to apikey: %s", b)
	}

	env.WriteClaudeJSON("alice@example.com", "org-1", "Acme")
	env.WriteCredentials("tok2", "ref2")
	env.Run("add", "acct").AssertSuccess(t)
	b, _ := os.ReadFile(env.StoreFile())
	if strings.Contains(string(b), `"apikey"`) || !strings.Contains(string(b), `"oauth"`) {
		t.Errorf("Source not updated back to oauth: %s", b)
	}
}

// A leftover apiKey next to a fresh OAuth login (what a swap followed by
// 'claude /login' leaves behind) must be saved as OAuth.
func TestAPIKey_LeftoverKeyDoesNotOverrideOAuthLogin(t *testing.T) {
	env := NewEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "")
	env.WriteClaudeJSON("erin@example.com", "org-9", "Acme")
	creds := filepath.Join(env.ClaudeConfigDir, ".credentials.json")
	env.writeJSON(creds, map[string]any{
		"apiKey": testAPIKey,
		"claudeAiOauth": map[string]any{
			"accessToken": "tok", "refreshToken": "ref", "expiresAt": 1893456000000,
		},
	}, 0o600)

	env.Run("add", "erin").AssertSuccess(t)
	if b, _ := os.ReadFile(env.StoreFile()); strings.Contains(string(b), `"apikey"`) {
		t.Errorf("erin was saved as an API-key profile: %s", b)
	}
}

// With an apiKeyHelper configured, the stored primaryApiKey is not the live
// key, so 'add' must refuse rather than save it.
func TestAPIKey_AddRefusesWhenAPIKeyHelperIsConfigured(t *testing.T) {
	env := NewEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "")
	env.writeAPIKeyLogin("bob@example.com", "org-2", testAPIKey)
	if err := os.WriteFile(filepath.Join(env.ClaudeConfigDir, "settings.json"),
		[]byte(`{"apiKeyHelper":"/usr/local/bin/get-key"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r := env.Run("add", "console")
	r.AssertError(t)
	r.AssertContains(t, "apiKeyHelper")
	if env.ProfileExists("console") {
		t.Error("a profile was saved from a possibly stale key")
	}
}
