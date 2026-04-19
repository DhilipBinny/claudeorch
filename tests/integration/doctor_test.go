//go:build integration

package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// TestDoctor_DriftReported pins the Phase 4 behaviour: when a profile's
// tokens_last_seen_at is older than the drift threshold (24h), doctor
// flags it. We simulate by editing store.json to an old timestamp.
func TestDoctor_DriftReported(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)

	data, err := os.ReadFile(env.StoreFile())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	profs := m["profiles"].(map[string]any)
	p := profs["work"].(map[string]any)
	// 48h ago — well past the 24h threshold.
	p["tokens_last_seen_at"] = time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339Nano)
	out, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(env.StoreFile(), out, 0o600); err != nil {
		t.Fatal(err)
	}

	r := env.Run("doctor")
	// doctor returns exit 1 when any check fails; drift is a failure.
	if r.ExitCode == 0 {
		t.Errorf("expected doctor to fail on drift; got OK\nstdout: %s", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "drift work") {
		t.Errorf("expected 'drift work' line, got:\n%s", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "claudeorch sync") {
		t.Errorf("expected sync suggestion, got:\n%s", r.Stdout)
	}
}

// TestDoctor_IsolateDirOrphanDetected pins the Phase 4 audit fix:
// when a profile is marked isolated but the isolate dir is missing
// (launch materialize failed, or user deleted it), doctor flags it.
func TestDoctor_IsolateDirOrphanDetected(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)

	// Plant Location=isolated in the store without creating the isolate dir.
	data, _ := os.ReadFile(env.StoreFile())
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	m["profiles"].(map[string]any)["work"].(map[string]any)["location"] = "isolated"
	out, _ := json.MarshalIndent(m, "", "  ")
	_ = os.WriteFile(env.StoreFile(), out, 0o600)

	r := env.Run("doctor")
	if r.ExitCode == 0 {
		t.Errorf("doctor should fail on isolate-dir orphan; got OK\n%s", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "isolate dir work") {
		t.Errorf("expected 'isolate dir work' entry:\n%s", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "claudeorch sync") {
		t.Errorf("expected sync suggestion in orphan message:\n%s", r.Stdout)
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
