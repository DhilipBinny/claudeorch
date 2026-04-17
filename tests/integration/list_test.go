//go:build integration

package integration

import (
	"strings"
	"testing"
)

func TestList_Empty(t *testing.T) {
	env := NewEnv(t)
	r := env.Run("list", "--no-usage")
	r.AssertSuccess(t)
	r.AssertContains(t, "No profiles")
}

func TestList_ShowsProfiles(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)

	env.WriteClaudeJSON("bob@corp.io", "org-uuid-2", "Corp")
	env.WriteCredentials("tok_b", "ref_b")
	env.Run("add", "home").AssertSuccess(t)

	r := env.Run("list", "--no-usage")
	r.AssertSuccess(t)
	r.AssertOutputContains(t, "work")
	r.AssertOutputContains(t, "home")
	r.AssertOutputContains(t, "alice@example.com")
	r.AssertOutputContains(t, "bob@corp.io")
}

func TestList_NoUsageFlag(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)

	r := env.Run("list", "--no-usage")
	r.AssertSuccess(t)
	// With --no-usage, usage columns should show "-"
	if !strings.Contains(r.Stdout, "-") {
		t.Errorf("expected '-' in output for unknown usage: %s", r.Stdout)
	}
}

func TestList_JSONOutput(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)

	r := env.Run("--json", "list", "--no-usage")
	r.AssertSuccess(t)
	r.AssertOutputContains(t, `"name"`)
	r.AssertOutputContains(t, `"work"`)
}
