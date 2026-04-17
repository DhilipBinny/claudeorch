//go:build integration

package integration

import (
	"os"
	"testing"
)

func TestPurge_WithForceYes(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)

	r := env.Run("--force", "purge", "--yes")
	r.AssertSuccess(t)
	r.AssertOutputContains(t, "Purge complete")

	// claudeorch home must not exist anymore.
	if _, err := os.Stat(env.ClaudeorchHome); err == nil {
		t.Error("claudeorch home still exists after purge")
	}
}

func TestPurge_WithoutConfirmCancels(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)

	// Non-TTY without --force --yes should error.
	r := env.Run("purge")
	r.AssertError(t)
	r.AssertContains(t, "confirmation")

	// Data must still be intact.
	if !env.ProfileExists("work") {
		t.Error("profile deleted without confirmation")
	}
}

func TestPurge_NeverTouchesClaudeDir(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)

	env.Run("--force", "purge", "--yes").AssertSuccess(t)

	// ClaudeConfigDir must still exist (we never touch it).
	if _, err := os.Stat(env.ClaudeConfigDir); err != nil {
		t.Error("ClaudeConfigDir was deleted by purge")
	}
}
