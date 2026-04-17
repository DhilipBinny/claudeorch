//go:build integration

package integration

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runWithFakeClaude runs claudeorch with a temp PATH that resolves `claude`
// to a shell script which echoes CLAUDE_CONFIG_DIR and its own arguments.
// Returns the captured stdout, stderr, and exit code.
func runWithFakeClaude(t *testing.T, env *Env, args ...string) RunResult {
	t.Helper()

	// Build a fake `claude` script in a dedicated temp dir.
	fakeDir := t.TempDir()
	script := `#!/bin/sh
echo "CLAUDE_CONFIG_DIR=$CLAUDE_CONFIG_DIR"
echo "ARGS: $*"
exit 0
`
	scriptPath := filepath.Join(fakeDir, "claude")
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(cliBin, args...)
	cmd.Env = append(os.Environ(),
		"CLAUDEORCH_HOME="+env.ClaudeorchHome,
		"CLAUDE_CONFIG_DIR="+env.ClaudeConfigDir,
		"PATH="+fakeDir+":"+os.Getenv("PATH"),
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			t.Fatalf("exec claudeorch %v: %v", args, err)
		}
	}
	return RunResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: code,
	}
}

func TestLaunch_ProfileNotFound(t *testing.T) {
	env := NewEnv(t)
	r := env.Run("launch", "ghost")
	r.AssertError(t)
	r.AssertContains(t, "not found")
}

func TestLaunch_InvalidName(t *testing.T) {
	env := NewEnv(t)
	r := env.Run("launch", "../bad")
	r.AssertError(t)
	r.AssertContains(t, "invalid")
}

func TestLaunch_Happy_SetsConfigDir(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)

	r := runWithFakeClaude(t, env, "launch", "work")
	r.AssertSuccess(t)

	// The fake claude script echoes CLAUDE_CONFIG_DIR; it must point at the
	// isolate dir, not at the original ClaudeConfigDir.
	expected := filepath.Join(env.ClaudeorchHome, "isolate", "work")
	if !strings.Contains(r.Stdout, "CLAUDE_CONFIG_DIR="+expected) {
		t.Errorf("CLAUDE_CONFIG_DIR not set to isolate dir\nexpected: %s\nstdout: %s",
			expected, r.Stdout)
	}
}

func TestLaunch_PassthroughArgs(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	env.Run("add", "work").AssertSuccess(t)

	r := runWithFakeClaude(t, env, "launch", "work", "--", "--version", "hello")
	r.AssertSuccess(t)

	if !strings.Contains(r.Stdout, "--version hello") {
		t.Errorf("passthrough args not forwarded to claude\nstdout: %s", r.Stdout)
	}
}

func TestLaunch_IsolatedFlag_NoSymlinks(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	// Seed a CLAUDE.md in ClaudeConfigDir so a non-isolated launch would link it.
	if err := os.WriteFile(filepath.Join(env.ClaudeConfigDir, "CLAUDE.md"),
		[]byte("shared memory"), 0o644); err != nil {
		t.Fatal(err)
	}
	env.Run("add", "work").AssertSuccess(t)

	r := runWithFakeClaude(t, env, "launch", "--isolated", "work")
	r.AssertSuccess(t)

	// In isolated mode, no CLAUDE.md symlink should be created.
	isolateDir := filepath.Join(env.ClaudeorchHome, "isolate", "work")
	claudeMd := filepath.Join(isolateDir, "CLAUDE.md")
	if _, err := os.Lstat(claudeMd); err == nil {
		t.Errorf("--isolated should not create CLAUDE.md symlink, but it exists at %s", claudeMd)
	}
}

func TestLaunch_NonIsolated_CreatesSymlinks(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("tok_a", "ref_a")
	// Seed a CLAUDE.md in ClaudeConfigDir so the launch can link it.
	claudeMdSrc := filepath.Join(env.ClaudeConfigDir, "CLAUDE.md")
	if err := os.WriteFile(claudeMdSrc, []byte("shared memory"), 0o644); err != nil {
		t.Fatal(err)
	}
	env.Run("add", "work").AssertSuccess(t)

	r := runWithFakeClaude(t, env, "launch", "work")
	r.AssertSuccess(t)

	// Default (non-isolated) mode should symlink CLAUDE.md.
	isolateDir := filepath.Join(env.ClaudeorchHome, "isolate", "work")
	claudeMdDst := filepath.Join(isolateDir, "CLAUDE.md")
	info, err := os.Lstat(claudeMdDst)
	if err != nil {
		t.Fatalf("CLAUDE.md symlink not created: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("CLAUDE.md at %s is not a symlink", claudeMdDst)
	}
}
