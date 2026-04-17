//go:build integration

package integration

import (
	"strings"
	"testing"
)

func TestStatus_Empty(t *testing.T) {
	env := NewEnv(t)
	r := env.Run("status", "--no-usage")
	r.AssertSuccess(t)
	r.AssertOutputContains(t, "Active profile: (none)")
	r.AssertOutputContains(t, "Sessions: (none)")
}

func TestStatus_WithActiveProfile_NoUsage(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)
	// 'add' marks the new profile active — no setActiveInStore needed.

	r := env.Run("status", "--no-usage")
	r.AssertSuccess(t)
	r.AssertOutputContains(t, "work")
	r.AssertOutputContains(t, "alice@example.com")
}

func TestStatus_NoActiveSessions(t *testing.T) {
	env := NewEnv(t)
	r := env.Run("status", "--no-usage")
	r.AssertSuccess(t)
	r.AssertOutputContains(t, "Sessions: (none)")
}

// TestStatus_TeasesOtherProfiles pins the "N other profiles. Run list..."
// footer from option-B: status shows 'me right now', list shows 'everyone'.
// The footer bridges discoverability.
func TestStatus_TeasesOtherProfiles(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)

	// Second profile with distinct identity.
	env.WriteClaudeJSON("bob@example.com", "org-uuid-2", "BobCo")
	env.WriteCredentials("tok_b", "ref_b")
	env.Run("add", "home").AssertSuccess(t)

	// Active is now 'home' (last add sets active). One OTHER profile: 'work'.
	r := env.Run("status", "--no-usage")
	r.AssertSuccess(t)
	r.AssertOutputContains(t, "1 other profile")
	r.AssertOutputContains(t, "claudeorch list")
}

func TestStatus_NoOtherProfiles_NoTeaser(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)

	r := env.Run("status", "--no-usage")
	r.AssertSuccess(t)
	if strings.Contains(r.Stdout, "other profile") {
		t.Errorf("status teased list when only one profile exists:\n%s", r.Stdout)
	}
}

// TestStatus_EmptyStore_TeasesList verifies that with no active profile but
// profiles present (edge case), we still nudge towards list.
func TestStatus_InactiveWithProfiles_Teases(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)

	// Forcibly clear active by rewriting the store without the active key.
	clearActiveInStore(t, env)

	r := env.Run("status", "--no-usage")
	r.AssertSuccess(t)
	r.AssertOutputContains(t, "Active profile: (none)")
	r.AssertOutputContains(t, "claudeorch list")
}
