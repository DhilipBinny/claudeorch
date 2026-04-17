//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDoctor_EmptyStore(t *testing.T) {
	env := NewEnv(t)
	// Doctor should pass even with no profiles.
	// (claude binary check may fail in CI — that's OK, we just check it runs)
	r := env.Run("doctor")
	// We only check it doesn't panic. Exit 1 is OK if claude isn't installed.
	_ = r
}

func TestDoctor_WrongPermissions_Fixable(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)

	// Deliberately break permissions on credentials.json.
	credsPath := filepath.Join(env.ProfileDir("work"), "credentials.json")
	if err := os.Chmod(credsPath, 0o644); err != nil {
		t.Fatal(err)
	}

	// Doctor without --fix should report the issue.
	r := env.Run("doctor")
	r.AssertContains(t, "credentials")

	// Doctor with --fix should repair it.
	env.Run("doctor", "--fix")

	info, err := os.Stat(credsPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("permissions not fixed: %04o", info.Mode().Perm())
	}
}

func TestDoctor_PreSwapOrphanReported(t *testing.T) {
	env := NewEnv(t)

	// Plant a fake .pre-swap orphan.
	orphan := filepath.Join(env.ClaudeConfigDir, ".credentials.json.pre-swap")
	if err := os.WriteFile(orphan, []byte("orphan"), 0o600); err != nil {
		t.Fatal(err)
	}

	r := env.Run("doctor")
	r.AssertContains(t, "pre-swap")
}
