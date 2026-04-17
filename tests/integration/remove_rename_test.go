//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"testing"
)

// ---- remove -----------------------------------------------------------------

func TestRemove_Happy(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)

	// 'add' marks the new profile active (it IS the live account on disk),
	// so plain 'remove' refuses for safety. Use --force for this test.
	env.Run("--force", "remove", "work").AssertSuccess(t)

	if env.ProfileExists("work") {
		t.Error("profile directory still exists after remove")
	}
	// List should show empty.
	r := env.Run("list", "--no-usage")
	r.AssertOutputContains(t, "No profiles")
}

func TestRemove_NotFound(t *testing.T) {
	env := NewEnv(t)
	env.Run("remove", "ghost").AssertError(t)
}

// TestRemove_AlsoCleansIsolateDir pins the regression from local testing:
// 'remove' used to leave ~/.claudeorch/isolate/<name>/.credentials.json on
// disk after the user explicitly asked for the profile to be removed.
// A credential copy surviving removal is a security bug.
func TestRemove_AlsoCleansIsolateDir(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)

	// Fake-materialize an isolate dir with a credentials file, to simulate
	// a prior 'launch work' having run.
	isolateDir := filepath.Join(env.ClaudeorchHome, "isolate", "work")
	if err := os.MkdirAll(isolateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	credsPath := filepath.Join(isolateDir, ".credentials.json")
	if err := os.WriteFile(credsPath, []byte(`{"claudeAiOauth":{"accessToken":"secret"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	env.Run("--force", "remove", "work").AssertSuccess(t)

	if _, err := os.Stat(isolateDir); !os.IsNotExist(err) {
		t.Errorf("isolate dir still exists after remove: %v", err)
	}
}

func TestRemove_ActiveRefusesWithoutForce(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)

	// Manually mark "work" as active by editing store.json.
	setActiveInStore(t, env, "work")

	r := env.Run("remove", "work")
	r.AssertError(t)
	r.AssertContains(t, "active")
	r.AssertContains(t, "--force")
}

func TestRemove_ActiveWithForce(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)
	setActiveInStore(t, env, "work")

	env.Run("--force", "remove", "work").AssertSuccess(t)
	if env.ProfileExists("work") {
		t.Error("profile still exists after forced remove")
	}
}

// ---- rename -----------------------------------------------------------------

func TestRename_Happy(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)

	env.Run("rename", "work", "office").AssertSuccess(t)

	if env.ProfileExists("work") {
		t.Error("old profile dir still exists")
	}
	if !env.ProfileExists("office") {
		t.Error("new profile dir not created")
	}
	r := env.Run("list", "--no-usage")
	r.AssertOutputContains(t, "office")
}

// TestRename_MovesIsolateDir pins the regression from local testing: rename
// used to leave ~/.claudeorch/isolate/<oldname>/ orphaned on disk, still
// holding a credential copy. The isolate dir must follow the profile name.
func TestRename_MovesIsolateDir(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)

	oldIsolate := filepath.Join(env.ClaudeorchHome, "isolate", "work")
	newIsolate := filepath.Join(env.ClaudeorchHome, "isolate", "office")
	if err := os.MkdirAll(oldIsolate, 0o700); err != nil {
		t.Fatal(err)
	}
	credsPath := filepath.Join(oldIsolate, ".credentials.json")
	if err := os.WriteFile(credsPath, []byte(`x`), 0o600); err != nil {
		t.Fatal(err)
	}

	env.Run("rename", "work", "office").AssertSuccess(t)

	if _, err := os.Stat(oldIsolate); !os.IsNotExist(err) {
		t.Errorf("old isolate dir should be gone, still exists")
	}
	if _, err := os.Stat(newIsolate); err != nil {
		t.Errorf("new isolate dir missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(newIsolate, ".credentials.json")); err != nil {
		t.Errorf("credentials missing from renamed isolate dir: %v", err)
	}
}

func TestRename_TargetExists(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)

	env.WriteClaudeJSON("bob@corp.io", "org-uuid-2", "Corp")
	env.WriteCredentials("tok_b", "ref_b")
	env.Run("add", "home").AssertSuccess(t)

	r := env.Run("rename", "work", "home")
	r.AssertError(t)
	r.AssertContains(t, "already exists")
}

func TestRename_NotFound(t *testing.T) {
	env := NewEnv(t)
	env.Run("rename", "ghost", "specter").AssertError(t)
}

func TestRename_InvalidNewName(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)

	env.Run("rename", "work", "../escape").AssertError(t)
}

func TestRename_UpdatesActivePointer(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)
	setActiveInStore(t, env, "work")

	env.Run("rename", "work", "office").AssertSuccess(t)

	// After rename, active pointer should be "office" not "work".
	r := env.Run("list", "--no-usage")
	r.AssertOutputContains(t, "office")
}
