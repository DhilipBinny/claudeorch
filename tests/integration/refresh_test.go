//go:build integration

package integration

import (
	"strings"
	"testing"
)

func TestRefresh_ProfileNotFound(t *testing.T) {
	env := NewEnv(t)
	r := env.Run("refresh", "ghost")
	r.AssertError(t)
	r.AssertContains(t, "not found")
}

func TestRefresh_InvalidName(t *testing.T) {
	env := NewEnv(t)
	r := env.Run("refresh", "../etc/passwd")
	r.AssertError(t)
	r.AssertContains(t, "invalid")
}

func TestRefresh_ActiveProfile_RefusedWithoutForce(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)

	// Mark work as active so refresh refuses without --force.
	setActiveInStore(t, env, "work")

	r := env.Run("refresh", "work")
	r.AssertError(t)
	r.AssertContains(t, "active")
}

func TestRefresh_ActiveProfile_ForcedButNetworkFails(t *testing.T) {
	// With --force, refresh attempts the OAuth call. Since we can't reach the
	// real endpoint from tests, expect a network-shaped error rather than an
	// "active profile" refusal. This proves --force overrides the safety gate.
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a_sufficiently_long_refresh_token_for_parse")
	env.Run("add", "work").AssertSuccess(t)
	setActiveInStore(t, env, "work")

	r := env.Run("--force", "refresh", "work")
	// Should not succeed (no real network), but should NOT refuse as "active".
	r.AssertError(t)
	if strings.Contains(r.Stderr, "use --force") || strings.Contains(r.Stdout, "use --force") {
		t.Errorf("--force did not override active-profile refusal:\nstdout: %s\nstderr: %s",
			r.Stdout, r.Stderr)
	}
}
