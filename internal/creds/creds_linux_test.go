//go:build linux

package creds

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// readMap decodes a credentials file into a generic map for assertions.
func readMap(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return m
}

// A real ~/.claude/.credentials.json also carries mcpOAuth tokens for MCP
// servers and plugins. A swap must not log the user out of those.
func TestMergeIntoFilePreservesForeignKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".credentials.json")
	existing := `{"mcpOAuth":{"plugin:vercel":{"accessToken":"keep-me"}},"claudeAiOauth":{"accessToken":"old"}}`
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	incoming := []byte(`{"claudeAiOauth":{"accessToken":"new"}}`)
	if err := mergeIntoFile(path, incoming); err != nil {
		t.Fatalf("mergeIntoFile: %v", err)
	}

	m := readMap(t, path)
	if _, ok := m["mcpOAuth"]; !ok {
		t.Error("mcpOAuth was dropped — swapping would deauthorize every MCP server")
	}
	var oauth struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(m["claudeAiOauth"], &oauth); err != nil {
		t.Fatal(err)
	}
	if oauth.AccessToken != "new" {
		t.Errorf("claudeAiOauth.accessToken = %q, want %q", oauth.AccessToken, "new")
	}
}

func TestMergeIntoFileCreatesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".credentials.json")
	if err := mergeIntoFile(path, []byte(`{"apiKey":"sk-ant-test"}`)); err != nil {
		t.Fatalf("mergeIntoFile: %v", err)
	}
	if got := readMap(t, path); string(got["apiKey"]) != `"sk-ant-test"` {
		t.Errorf("apiKey = %s", got["apiKey"])
	}
}

// A corrupt existing file must not block a swap.
func TestMergeIntoFileOverwritesCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".credentials.json")
	if err := os.WriteFile(path, []byte("not json at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mergeIntoFile(path, []byte(`{"apiKey":"sk-ant-test"}`)); err != nil {
		t.Fatalf("mergeIntoFile: %v", err)
	}
	if got := readMap(t, path); string(got["apiKey"]) != `"sk-ant-test"` {
		t.Errorf("apiKey = %s", got["apiKey"])
	}
}

func TestMergeIntoFileWritesOwnerOnlyPerms(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".credentials.json")
	if err := mergeIntoFile(path, []byte(`{"apiKey":"sk-ant-test"}`)); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("perm = %o, want 600", perm)
	}
}

func TestDeleteFileKeysRemovesOnlyNamedKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".credentials.json")
	existing := `{"mcpOAuth":{"a":1},"claudeAiOauth":{"accessToken":"x"},"apiKey":"sk-ant-x"}`
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := deleteFileKeys(path, "claudeAiOauth"); err != nil {
		t.Fatalf("deleteFileKeys: %v", err)
	}
	m := readMap(t, path)
	if _, ok := m["claudeAiOauth"]; ok {
		t.Error("claudeAiOauth should have been removed")
	}
	if _, ok := m["mcpOAuth"]; !ok {
		t.Error("mcpOAuth should have been preserved")
	}
	if _, ok := m["apiKey"]; !ok {
		t.Error("apiKey should have been preserved")
	}
}

func TestDeleteFileKeysMissingFileIsNotAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.json")
	if err := deleteFileKeys(path, "claudeAiOauth"); err != nil {
		t.Errorf("want nil for missing file, got %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("deleteFileKeys must not create the file")
	}
}

func TestExtractAPIKey(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"bare string", "sk-ant-abc", "sk-ant-abc"},
		{"trailing newline", "sk-ant-abc\n", "sk-ant-abc"},
		{"json envelope", `{"apiKey":"sk-ant-abc"}`, "sk-ant-abc"},
		{"json without apiKey", `{"other":"x"}`, ""},
		{"malformed json", `{broken`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractAPIKey([]byte(tc.in)); got != tc.want {
				t.Errorf("extractAPIKey(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestLooksLikeJSONObject(t *testing.T) {
	if !looksLikeJSONObject([]byte(`  {"a":1}`)) {
		t.Error("leading whitespace + object should qualify")
	}
	if looksLikeJSONObject([]byte("sk-ant-abc")) {
		t.Error("a bare API key must not be mistaken for an OAuth blob")
	}
}

// The test guard is load-bearing: without it any test reaching
// ReadLive/WriteLive would read or destroy the developer's real keyring.
func TestSecretServiceDisabledInsideTests(t *testing.T) {
	if !secretServiceDisabled() {
		t.Fatal("secretServiceDisabled() must be true inside a test binary")
	}
	if secretToolAvailable() {
		t.Fatal("secretToolAvailable() must be false inside a test binary")
	}
	if IsKeychainBased() {
		t.Fatal("IsKeychainBased() must be false inside a test binary")
	}
}
