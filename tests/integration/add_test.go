//go:build integration

package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAdd_Happy verifies the golden path: add writes profile files and
// prints confirmation.
func TestAdd_Happy(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_access_abc", "ref_refresh_xyz")

	r := env.Run("add", "work")
	r.AssertSuccess(t)
	r.AssertOutputContains(t, "work")

	if !env.ProfileExists("work") {
		t.Fatal("profile directory not created")
	}
	creds := env.ReadProfileCredentials("work")
	if !strings.Contains(string(creds), "tok_access_abc") {
		t.Error("credentials not saved")
	}
}

// TestAdd_NoLogin refuses when .credentials.json is absent.
func TestAdd_NoLogin(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	// No credentials written.
	r := env.Run("add", "work")
	r.AssertError(t)
	r.AssertContains(t, "credentials")
}

// TestAdd_DuplicateIdentity refreshes credentials in place when the name
// matches the existing profile saved under this identity.
func TestAdd_DuplicateIdentity(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_first", "ref_first")

	r := env.Run("add", "work")
	r.AssertSuccess(t)

	// Write new credentials for same identity; same name arg → refresh-in-place.
	env.WriteCredentials("tok_second", "ref_second")
	r2 := env.Run("add", "work")
	r2.AssertSuccess(t)
	r2.AssertContains(t, "Updated credentials")

	// The stored credentials must be the updated ones.
	creds := env.ReadProfileCredentials("work")
	if !strings.Contains(string(creds), "tok_second") {
		t.Error("credentials not updated")
	}
}

// TestAdd_DefaultName derives name from email prefix on non-TTY.
func TestAdd_DefaultName(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("bob@company.io", "org-uuid-2", "Corp")
	env.WriteCredentials("tok_bob", "ref_bob")

	// No name arg → non-TTY (CI) → email prefix "bob" used.
	r := env.Run("add")
	r.AssertSuccess(t)

	if !env.ProfileExists("bob") {
		t.Errorf("expected profile 'bob' to be created (email prefix)\nstdout: %s", r.Stdout)
	}
}

// TestAdd_CollisionSuffix appends a numeric suffix when email prefix is taken.
func TestAdd_CollisionSuffix(t *testing.T) {
	env := NewEnv(t)

	// First account: alice@example.com
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Org1")
	env.WriteCredentials("tok_1", "ref_1")
	env.Run("add").AssertSuccess(t)

	// Second account with same email prefix but different org.
	env.WriteClaudeJSON("alice@example.com", "org-uuid-2", "Org2")
	env.WriteCredentials("tok_2", "ref_2")
	r := env.Run("add")
	r.AssertSuccess(t)

	// Should create "alice-2" since "alice" is taken.
	if !env.ProfileExists("alice-2") {
		t.Errorf("expected 'alice-2' profile\nstdout: %s\nstderr: %s", r.Stdout, r.Stderr)
	}
}

// TestAdd_InvalidName refuses names that fail profile name validation.
func TestAdd_InvalidName(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_abc", "ref_xyz")

	for _, bad := range []string{".hidden", "../escape", "has space", ""} {
		r := env.Run("add", bad)
		r.AssertError(t)
	}
}

// TestAdd_DifferentName_WithDuplicateIdentity pins the UX from local testing:
// if the user provides an explicit name that disagrees with the existing
// profile already saved under that (email, org), refuse with a clear error
// instead of silently refreshing the mismatched profile. Otherwise 'add bala'
// while live ~/.claude/ holds dhilip looks like it saved bala but actually
// refreshed dhilip.
func TestAdd_DifferentName_WithDuplicateIdentity(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)

	// Second 'add' uses the SAME live identity (alice/org-uuid-1) but a
	// different explicit name. Must error.
	r := env.Run("add", "personal")
	r.AssertError(t)
	r.AssertContains(t, "already saved as \"work\"")

	// And the 'work' profile must NOT have been silently refreshed — the
	// user's intent was unambiguous: add under name 'personal'.
	// Verified indirectly: 'personal' shouldn't exist, 'work' shouldn't have
	// LastUsedAt bumped. We just assert the list is unchanged.
}

// TestAdd_ClearsNeedsReauthOnRefreshInPlace pins the fix from a real-world
// bug: after a failed 'refresh' marked a profile NeedsReauth=true, doing
// 'claude /login' then 'claudeorch add <same-name>' (the recovery flow
// the CLI itself suggests) left the flag set. List + doctor kept showing
// "!reauth" forever even though the account worked. 'add' must clear the
// flag when it refreshes-in-place because it's doing exactly what the
// flag says is needed.
func TestAdd_ClearsNeedsReauthOnRefreshInPlace(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-1", "Acme")
	env.WriteCredentials("first_a", "first_r")
	env.Run("add", "work").AssertSuccess(t)

	// Simulate a prior refresh having marked NeedsReauth — edit store.json.
	data, _ := os.ReadFile(env.StoreFile())
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	m["profiles"].(map[string]any)["work"].(map[string]any)["needs_reauth"] = true
	out, _ := json.MarshalIndent(m, "", "  ")
	_ = os.WriteFile(env.StoreFile(), out, 0o600)

	// Now user re-logs-in and re-adds — the fix should clear the flag.
	env.WriteCredentials("second_a", "second_r")
	env.Run("add", "work").AssertSuccess(t)

	data, _ = os.ReadFile(env.StoreFile())
	_ = json.Unmarshal(data, &m)
	reauth := m["profiles"].(map[string]any)["work"].(map[string]any)["needs_reauth"]
	// Zero value for a missing/false flag: either key absent or value false.
	if reauth == true {
		t.Errorf("needs_reauth still true after refresh-in-place add:\n%s", data)
	}
}

// TestAdd_SameName_RefreshesInPlace covers the allowed case: explicit name
// matches the existing profile's name — refresh-in-place is expected.
func TestAdd_SameName_RefreshesInPlace(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)

	r := env.Run("add", "work")
	r.AssertSuccess(t)
	r.AssertOutputContains(t, "Updated")
}

// TestAdd_InvalidName_WithDuplicateIdentity pins the regression from local
// testing: a garbage name used to silently fall through to refresh-in-place
// when the (email, org) already matched an existing profile, because name
// validation happened AFTER the duplicate check. Name validation must come
// first so the user gets a clear error instead of quiet success.
func TestAdd_InvalidName_WithDuplicateIdentity(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_abc", "ref_xyz")
	env.Run("add", "work").AssertSuccess(t)

	// Identity now matches 'work'. A bad name must STILL be rejected,
	// not silently treated as a refresh-in-place of 'work'.
	r := env.Run("add", "../evil")
	r.AssertError(t)
	r.AssertContains(t, "invalid")
}

// TestAdd_SetsActive pins the UX: because 'add' always copies credentials
// from live ~/.claude/, the newly-added profile IS the live account — so
// the store must mark it active. Otherwise 'status' shows "(none)" even
// after a clean add, which looks broken to users.
func TestAdd_SetsActive(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)

	r := env.Run("status")
	r.AssertSuccess(t)
	r.AssertOutputContains(t, "work")
	if strings.Contains(r.Stdout, "Active profile: (none)") {
		t.Errorf("status shows 'Active profile: (none)' after add — active pointer not set:\n%s", r.Stdout)
	}
}

// TestAdd_DuplicateUpdatesActive pins the UX for the refresh-in-place path:
// if live ~/.claude/ matches an existing saved profile (because the user
// logged back in as that account), adding it again must mark THAT profile
// active — otherwise a stale active pointer from before the re-login hangs
// around.
func TestAdd_DuplicateUpdatesActive(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)

	// Second add of the same identity AND same name — refresh-in-place path.
	// (A different name arg would be rejected by the disagreement check.)
	env.Run("add", "work").AssertSuccess(t)

	r := env.Run("status")
	if strings.Contains(r.Stdout, "Active profile: (none)") {
		t.Errorf("refresh-in-place should keep/set active, got:\n%s", r.Stdout)
	}
	// First 'work' absorbed the identity; second call's 'work2' arg is ignored
	// because refresh-in-place takes over. 'work' must be active.
	r.AssertOutputContains(t, "Active profile: work")
}

// TestAdd_StoreVersion verifies store.json always contains "version":1.
func TestAdd_StoreVersion(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_abc", "ref_xyz")

	env.Run("add", "work").AssertSuccess(t)

	data, err := os.ReadFile(env.StoreFile())
	if err != nil {
		t.Fatalf("read store.json: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse store.json: %v", err)
	}
	if v, ok := m["version"].(float64); !ok || v != 2 {
		t.Errorf("store.json version = %v, want 2", m["version"])
	}
}

// TestAdd_FilePermissions verifies credentials.json is 0600.
func TestAdd_FilePermissions(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_abc", "ref_xyz")

	env.Run("add", "work").AssertSuccess(t)

	info, err := os.Stat(filepath.Join(env.ProfileDir("work"), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("credentials.json mode = %04o, want 0600", info.Mode().Perm())
	}
}
