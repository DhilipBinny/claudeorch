//go:build integration

package integration

import (
	"testing"
)

func TestStatus_Empty(t *testing.T) {
	env := NewEnv(t)
	r := env.Run("status")
	r.AssertSuccess(t)
	r.AssertOutputContains(t, "Active profile: (none)")
	r.AssertOutputContains(t, "Sessions: (none)")
}

func TestStatus_WithActiveProfile(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)
	setActiveInStore(t, env, "work")

	r := env.Run("status")
	r.AssertSuccess(t)
	r.AssertOutputContains(t, "work")
	r.AssertOutputContains(t, "alice@example.com")
}

func TestStatus_NoActiveSessions(t *testing.T) {
	env := NewEnv(t)
	r := env.Run("status")
	r.AssertSuccess(t)
	r.AssertOutputContains(t, "Sessions: (none)")
}
