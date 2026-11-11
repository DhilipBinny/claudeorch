package creds

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeClaudeJSON points CLAUDE_CONFIG_DIR at a temp dir containing a
// .claude.json with the given primaryApiKey and approval lists.
func writeClaudeJSON(t *testing.T, primary string, approved, rejected []string) {
	t.Helper()
	dir := t.TempDir()
	doc := map[string]any{
		"primaryApiKey": primary,
		"customApiKeyResponses": map[string]any{
			"approved": approved,
			"rejected": rejected,
		},
	}
	blob, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), blob, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
}

func TestApprovalForMatchesBySuffix(t *testing.T) {
	// Claude Code stores only a trailing fragment of each key.
	writeClaudeJSON(t, "", []string{"TAIL-APPROVED"}, []string{"TAIL-REJECTED"})
	auth := readClaudeJSONAuth()

	if got := auth.approvalFor("sk-ant-api03-xxxxTAIL-APPROVED"); got != approvalApproved {
		t.Errorf("approved key: got %v", got)
	}
	if got := auth.approvalFor("sk-ant-api03-xxxxTAIL-REJECTED"); got != approvalRejected {
		t.Errorf("rejected key: got %v", got)
	}
	if got := auth.approvalFor("sk-ant-api03-unrelated"); got != approvalUnknown {
		t.Errorf("unlisted key: got %v", got)
	}
	if got := auth.approvalFor(""); got != approvalUnknown {
		t.Errorf("empty key: got %v", got)
	}
}

// A key in both lists must be treated as rejected — refusing is the safe
// reading of contradictory state.
func TestApprovalForPrefersRejection(t *testing.T) {
	writeClaudeJSON(t, "", []string{"SAME-TAIL"}, []string{"SAME-TAIL"})
	if got := readClaudeJSONAuth().approvalFor("sk-ant-api03-SAME-TAIL"); got != approvalRejected {
		t.Errorf("got %v, want rejected", got)
	}
}

// The real-world case on an API-key install: the user declined the key
// exported from their shell rc, so Claude Code uses the primaryApiKey it
// provisioned at login. Returning the rejected env key here would save a
// credential Claude Code refuses to use.
func TestLiveAPIKeyIgnoresRejectedEnvKey(t *testing.T) {
	writeClaudeJSON(t, "sk-ant-api03-PROVISIONED", nil, []string{"FROM-ZSHRC"})
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-api03-FROM-ZSHRC")

	key, source, ok := liveAPIKeyFromClaudeState()
	if !ok {
		t.Fatal("expected a key")
	}
	if key != "sk-ant-api03-PROVISIONED" {
		t.Errorf("key = %q, want the provisioned key", key)
	}
	if source != APIKeySourceClaudeJSON {
		t.Errorf("source = %q, want %q", source, APIKeySourceClaudeJSON)
	}
}

// An approved env key overrides anything on disk, matching Claude Code.
func TestLiveAPIKeyPrefersApprovedEnvKey(t *testing.T) {
	writeClaudeJSON(t, "sk-ant-api03-PROVISIONED", []string{"FROM-ZSHRC"}, nil)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-api03-FROM-ZSHRC")

	key, source, ok := liveAPIKeyFromClaudeState()
	if !ok {
		t.Fatal("expected a key")
	}
	if key != "sk-ant-api03-FROM-ZSHRC" {
		t.Errorf("key = %q, want the env key", key)
	}
	if source != APIKeySourceEnv {
		t.Errorf("source = %q, want %q", source, APIKeySourceEnv)
	}
}

// No verdict recorded: prefer what Claude Code is demonstrably using.
func TestLiveAPIKeyPrefersPrimaryOverUnknownEnvKey(t *testing.T) {
	writeClaudeJSON(t, "sk-ant-api03-PROVISIONED", nil, nil)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-api03-UNJUDGED")

	key, source, ok := liveAPIKeyFromClaudeState()
	if !ok {
		t.Fatal("expected a key")
	}
	if key != "sk-ant-api03-PROVISIONED" || source != APIKeySourceClaudeJSON {
		t.Errorf("got (%q, %q), want the provisioned key", key, source)
	}
}

// Last resort: an unjudged env key is better than nothing.
func TestLiveAPIKeyFallsBackToUnjudgedEnvKey(t *testing.T) {
	writeClaudeJSON(t, "", nil, nil)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-api03-UNJUDGED")

	key, source, ok := liveAPIKeyFromClaudeState()
	if !ok {
		t.Fatal("expected a key")
	}
	if key != "sk-ant-api03-UNJUDGED" || source != APIKeySourceEnv {
		t.Errorf("got (%q, %q), want the env key", key, source)
	}
}

func TestLiveAPIKeyRejectedEnvWithNoPrimaryYieldsNothing(t *testing.T) {
	writeClaudeJSON(t, "", nil, []string{"FROM-ZSHRC"})
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-api03-FROM-ZSHRC")

	if key, _, ok := liveAPIKeyFromClaudeState(); ok {
		t.Errorf("expected no key, got %q", key)
	}
}

func TestLiveAPIKeyNoSourcesAtAll(t *testing.T) {
	writeClaudeJSON(t, "", nil, nil)
	t.Setenv("ANTHROPIC_API_KEY", "")
	if key, _, ok := liveAPIKeyFromClaudeState(); ok {
		t.Errorf("expected no key, got %q", key)
	}
}

func TestEnvAPIKeyRejected(t *testing.T) {
	writeClaudeJSON(t, "", nil, []string{"FROM-ZSHRC"})

	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-api03-FROM-ZSHRC")
	if !EnvAPIKeyRejected() {
		t.Error("want true for a declined env key")
	}

	t.Setenv("ANTHROPIC_API_KEY", "")
	if EnvAPIKeyRejected() {
		t.Error("want false when no env key is set")
	}
}

// A rejected env key shadows nothing, because Claude Code ignores it.
func TestEnvAPIKeyShadowsProfiles(t *testing.T) {
	writeClaudeJSON(t, "", []string{"APPROVED-TAIL"}, []string{"REJECTED-TAIL"})

	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-api03-REJECTED-TAIL")
	if EnvAPIKeyShadowsProfiles() {
		t.Error("a rejected env key must not count as shadowing")
	}

	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-api03-APPROVED-TAIL")
	if !EnvAPIKeyShadowsProfiles() {
		t.Error("an approved env key shadows stored profiles")
	}

	t.Setenv("ANTHROPIC_API_KEY", "")
	if EnvAPIKeyShadowsProfiles() {
		t.Error("no env key means no shadowing")
	}
}

func TestReadClaudeJSONAuthMissingFileIsZeroValue(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	auth := readClaudeJSONAuth()
	if auth.PrimaryAPIKey != "" || len(auth.CustomAPIKeyResponses.Approved) != 0 {
		t.Errorf("want zero value, got %+v", auth)
	}
}

func TestReadClaudeJSONAuthCorruptFileIsZeroValue(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	if auth := readClaudeJSONAuth(); auth.PrimaryAPIKey != "" {
		t.Errorf("want zero value, got %+v", auth)
	}
}
