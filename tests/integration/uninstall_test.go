//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"testing"
)

// TestUninstall_FullFlow_Yes verifies a non-interactive uninstall with --yes:
// zero-overwrite + remove claudeorch home, leave ~/.claude/ intact.
//
// Binary removal is gated by --keep-binary here because the integration binary
// is shared across tests.
func TestUninstall_FullFlow_Yes(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)

	// Sanity: claudeorch home exists, claude dir exists.
	if _, err := os.Stat(env.ClaudeorchHome); err != nil {
		t.Fatalf("claudeorch home missing pre-uninstall: %v", err)
	}

	r := env.Run("uninstall", "--yes", "--keep-binary")
	r.AssertSuccess(t)

	// claudeorch home should be gone.
	if _, err := os.Stat(env.ClaudeorchHome); !os.IsNotExist(err) {
		t.Errorf("claudeorch home still exists after uninstall: %v", err)
	}
	// claude dir must still be intact.
	if _, err := os.Stat(env.ClaudeConfigDir); err != nil {
		t.Errorf("~/.claude/ was touched by uninstall: %v", err)
	}
	// ~/.claude.json must still be intact.
	claudeJSON := filepath.Join(env.ClaudeConfigDir, ".claude.json")
	if _, err := os.Stat(claudeJSON); err != nil {
		t.Errorf("~/.claude.json was removed by uninstall: %v", err)
	}
}

// TestUninstall_WithoutYes_Refuses_NonTTY guards the confirmation path.
func TestUninstall_WithoutYes_Refuses_NonTTY(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)

	r := env.Run("uninstall")
	r.AssertError(t)
	r.AssertContains(t, "confirmation")

	// Nothing should have been removed.
	if _, err := os.Stat(env.ClaudeorchHome); err != nil {
		t.Errorf("claudeorch home removed without confirmation: %v", err)
	}
}

// TestUninstall_KeepState_RemovesBinaryOnly validates --keep-state.
// We can't actually remove the shared test binary, so we also pass
// --keep-binary to make this an effective no-op and just verify the flag
// combination doesn't error.
func TestUninstall_KeepState_ErrorsWithBothKeepFlags(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)

	r := env.Run("uninstall", "--yes", "--keep-binary", "--keep-state")
	r.AssertError(t)
	r.AssertContains(t, "no-op")
}

// TestUninstall_RemovesStatuslineEntry verifies that the statusLine entry
// gets cleaned from ~/.claude/settings.json when it belongs to claudeorch.
func TestUninstall_RemovesStatuslineEntry(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)

	settingsPath := filepath.Join(env.ClaudeConfigDir, "settings.json")
	payload := `{"other":"keep","statusLine":{"type":"command","command":"/fake/path claudeorch statusline"}}`
	if err := os.WriteFile(settingsPath, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}

	env.Run("uninstall", "--yes", "--keep-binary").AssertSuccess(t)

	// settings.json should have 'other' preserved, 'statusLine' removed.
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	s := string(data)
	if !contains(s, "\"other\"") {
		t.Errorf("'other' key was dropped, should have been preserved:\n%s", s)
	}
	if contains(s, "\"statusLine\"") {
		t.Errorf("statusLine was not removed:\n%s", s)
	}
}

// TestUninstall_KeepsForeignStatusLine verifies we don't clobber a statusLine
// that wasn't set by claudeorch.
func TestUninstall_KeepsForeignStatusLine(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)

	settingsPath := filepath.Join(env.ClaudeConfigDir, "settings.json")
	payload := `{"statusLine":{"type":"command","command":"/usr/local/bin/my-custom-statusline"}}`
	if err := os.WriteFile(settingsPath, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}

	env.Run("uninstall", "--yes", "--keep-binary").AssertSuccess(t)

	data, _ := os.ReadFile(settingsPath)
	if !contains(string(data), "my-custom-statusline") {
		t.Errorf("foreign statusLine was wrongly removed:\n%s", data)
	}
}

// contains is a tiny helper to avoid pulling in strings for one check.
func contains(haystack, needle string) bool {
	if len(needle) > len(haystack) {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
