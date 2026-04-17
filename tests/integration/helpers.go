//go:build integration

// Package integration provides end-to-end tests for claudeorch commands.
// Each test runs the claudeorch binary (built by TestMain) against synthetic
// Claude state in temp dirs so no real credentials are touched.
//
// Run with: go test -tags=integration ./tests/integration/...
package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// cliBin is the path to the built claudeorch binary. Set by TestMain.
var cliBin string

// Env is a test environment with isolated temp dirs for claudeorch and Claude state.
type Env struct {
	ClaudeorchHome  string
	ClaudeConfigDir string
	t               *testing.T
}

// NewEnv creates a fresh isolated environment. Env vars are restored on cleanup.
func NewEnv(t *testing.T) *Env {
	t.Helper()
	orchHome := t.TempDir()
	claudeDir := t.TempDir()
	t.Setenv("CLAUDEORCH_HOME", orchHome)
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	return &Env{
		ClaudeorchHome:  orchHome,
		ClaudeConfigDir: claudeDir,
		t:               t,
	}
}

// WriteCredentials writes a synthetic .credentials.json into ClaudeConfigDir.
func (e *Env) WriteCredentials(accessToken, refreshToken string) {
	e.t.Helper()
	payload := map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken":      accessToken,
			"refreshToken":     refreshToken,
			"expiresAt":        time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			"scopes":           []string{"openai"},
			"subscriptionType": "pro",
		},
	}
	e.writeJSON(filepath.Join(e.ClaudeConfigDir, ".credentials.json"), payload, 0o600)
}

// WriteClaudeJSON writes a synthetic .claude.json into ClaudeConfigDir.
func (e *Env) WriteClaudeJSON(email, orgUUID, orgName string) {
	e.t.Helper()
	payload := map[string]any{
		"numStartups": 1,
		"oauthAccount": map[string]any{
			"emailAddress":     email,
			"organizationUuid": orgUUID,
			"organizationName": orgName,
		},
	}
	e.writeJSON(filepath.Join(e.ClaudeConfigDir, ".claude.json"), payload, 0o600)
}

func (e *Env) writeJSON(path string, v any, mode os.FileMode) {
	e.t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		e.t.Fatalf("marshal JSON for %s: %v", path, err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		e.t.Fatalf("write %s: %v", path, err)
	}
}

// RunResult holds the output of a command invocation.
type RunResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Run executes claudeorch with the given arguments.
func (e *Env) Run(args ...string) RunResult {
	e.t.Helper()
	cmd := exec.Command(cliBin, args...)
	cmd.Env = append(os.Environ(),
		"CLAUDEORCH_HOME="+e.ClaudeorchHome,
		"CLAUDE_CONFIG_DIR="+e.ClaudeConfigDir,
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
			e.t.Fatalf("exec claudeorch %v: %v", args, err)
		}
	}
	return RunResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: code,
	}
}

// AssertSuccess fails the test if the command returned a non-zero exit code.
func (r RunResult) AssertSuccess(t *testing.T) {
	t.Helper()
	if r.ExitCode != 0 {
		t.Fatalf("command failed (exit %d)\nstdout: %s\nstderr: %s",
			r.ExitCode, r.Stdout, r.Stderr)
	}
}

// AssertError fails the test if the command succeeded.
func (r RunResult) AssertError(t *testing.T) {
	t.Helper()
	if r.ExitCode == 0 {
		t.Fatalf("expected command to fail but it succeeded\nstdout: %s\nstderr: %s",
			r.Stdout, r.Stderr)
	}
}

// AssertContains fails if s does not appear in stdout or stderr.
func (r RunResult) AssertContains(t *testing.T, s string) {
	t.Helper()
	if !strings.Contains(r.Stdout, s) && !strings.Contains(r.Stderr, s) {
		t.Errorf("expected output to contain %q\nstdout: %s\nstderr: %s", s, r.Stdout, r.Stderr)
	}
}

// AssertOutputContains fails if s does not appear in stdout.
func (r RunResult) AssertOutputContains(t *testing.T, s string) {
	t.Helper()
	if !strings.Contains(r.Stdout, s) {
		t.Errorf("expected stdout to contain %q, got: %s", s, r.Stdout)
	}
}

// StoreFile returns the path to store.json in the test environment.
func (e *Env) StoreFile() string {
	return filepath.Join(e.ClaudeorchHome, "store.json")
}

// ProfileDir returns the path to a named profile dir.
func (e *Env) ProfileDir(name string) string {
	return filepath.Join(e.ClaudeorchHome, "profiles", name)
}

// ProfileExists reports whether the named profile directory exists.
func (e *Env) ProfileExists(name string) bool {
	_, err := os.Stat(e.ProfileDir(name))
	return err == nil
}

// ReadProfileCredentials reads and returns credentials.json from a profile.
func (e *Env) ReadProfileCredentials(name string) []byte {
	e.t.Helper()
	data, err := os.ReadFile(filepath.Join(e.ProfileDir(name), "credentials.json"))
	if err != nil {
		e.t.Fatalf("read credentials for profile %s: %v", name, err)
	}
	return data
}
