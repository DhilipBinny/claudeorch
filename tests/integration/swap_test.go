//go:build integration

package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSwap_Happy(t *testing.T) {
	env := NewEnv(t)

	// Add two profiles.
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_alice", "ref_alice")
	env.Run("add", "work").AssertSuccess(t)

	env.WriteClaudeJSON("bob@corp.io", "org-uuid-2", "Corp")
	env.WriteCredentials("tok_bob", "ref_bob")
	env.Run("add", "home").AssertSuccess(t)

	// Swap to work.
	r := env.Run("swap", "work")
	r.AssertSuccess(t)
	r.AssertOutputContains(t, "work")

	// Live credentials file must contain alice's token.
	credsPath := filepath.Join(env.ClaudeConfigDir, ".credentials.json")
	creds, err := os.ReadFile(credsPath)
	if err != nil {
		t.Fatalf("read live credentials: %v", err)
	}
	if !strings.Contains(string(creds), "tok_alice") {
		t.Errorf("live credentials don't contain alice's token after swap: %s", creds)
	}
}

func TestSwap_RefusesWithRunningSession(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_alice", "ref_alice")
	env.Run("add", "work").AssertSuccess(t)

	// Seed a live session file with our own PID.
	sessDir := filepath.Join(env.ClaudeConfigDir, "sessions")
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatal(err)
	}
	seededSession := `{"pid":` + itoa(os.Getpid()) + `,"sessionId":"test","cwd":"/tmp"}`
	if err := os.WriteFile(filepath.Join(sessDir, "live.json"), []byte(seededSession), 0o600); err != nil {
		t.Fatal(err)
	}

	r := env.Run("swap", "work")
	// Must exit 2 (not 1 — not a generic error).
	if r.ExitCode != 2 {
		t.Errorf("expected exit 2 for running-session refuse, got %d\nstdout: %s\nstderr: %s",
			r.ExitCode, r.Stdout, r.Stderr)
	}
}

func TestSwap_NotFound(t *testing.T) {
	env := NewEnv(t)
	env.Run("swap", "ghost").AssertError(t)
}

func TestSwap_ActiveUpdatedInStore(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)

	env.Run("swap", "work").AssertSuccess(t)

	r := env.Run("status")
	r.AssertSuccess(t)
	r.AssertOutputContains(t, "work")
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}
