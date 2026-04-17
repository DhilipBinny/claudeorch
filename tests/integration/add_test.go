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

// TestAdd_DuplicateIdentity refreshes credentials in place and does not create a second profile.
func TestAdd_DuplicateIdentity(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_first", "ref_first")

	r := env.Run("add", "work")
	r.AssertSuccess(t)

	// Write new credentials for same identity.
	env.WriteCredentials("tok_second", "ref_second")
	r2 := env.Run("add", "work2")
	r2.AssertSuccess(t)
	r2.AssertContains(t, "Updated credentials")

	// The "work2" profile must NOT have been created — identity matched "work".
	if env.ProfileExists("work2") {
		t.Error("duplicate profile was created instead of updating existing")
	}
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
	if v, ok := m["version"].(float64); !ok || v != 1 {
		t.Errorf("store.json version = %v, want 1", m["version"])
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
