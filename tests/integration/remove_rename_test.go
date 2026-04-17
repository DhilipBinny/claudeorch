//go:build integration

package integration

import (
	"testing"
)

// ---- remove -----------------------------------------------------------------

func TestRemove_Happy(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)

	env.Run("remove", "work").AssertSuccess(t)

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
