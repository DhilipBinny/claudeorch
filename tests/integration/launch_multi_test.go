//go:build integration

package integration

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// holdingClaude is a fake `claude` that prints where it runs and, when
// HOLD_FILE exists, stays alive until the test removes that file. It lets a
// test keep a "running session" for a profile while launching it again.
const holdingClaude = `#!/bin/sh
echo "PWD=$(pwd)"
echo "CLAUDE_CONFIG_DIR=$CLAUDE_CONFIG_DIR"
echo "ARGS: $*"
if [ -n "$HOLD_FILE" ]; then
  while [ -e "$HOLD_FILE" ]; do sleep 0.1; done
fi
exit 0
`

// fakeClaudePath writes holdingClaude into a temp dir and returns the dir.
func fakeClaudePath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(holdingClaude), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// launchCmd builds a claudeorch command wired to env and the fake claude.
func launchCmd(env *Env, fakeDir, holdFile string, args ...string) (*exec.Cmd, *bytes.Buffer, *bytes.Buffer) {
	cmd := exec.Command(cliBin, args...)
	cmd.Env = append(os.Environ(),
		"CLAUDEORCH_HOME="+env.ClaudeorchHome,
		"CLAUDE_CONFIG_DIR="+env.ClaudeConfigDir,
		"PATH="+fakeDir+":"+os.Getenv("PATH"),
		"HOLD_FILE="+holdFile,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	return cmd, &stdout, &stderr
}

// TestLaunch_SameProfileTwiceFromDifferentFolders is the regression test for
// "launch refuses a second session of the same profile". A profile with a
// live session must accept another launch (any folder), joining the same
// isolate dir, like plain `claude` does.
func TestLaunch_SameProfileTwiceFromDifferentFolders(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("session ownership detection via /proc is Linux-only")
	}
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-1", "Acme")
	env.WriteCredentials("tok", "ref")
	env.Run("add", "work").AssertSuccess(t)
	// Take the profile out of ~/.claude/ so it is not "live" there; that
	// separate double-copy case stays refused (TestLaunch_RefusesWhenProfileIsLive).
	_ = os.Remove(filepath.Join(env.ClaudeConfigDir, ".credentials.json"))
	_ = os.Remove(filepath.Join(env.ClaudeConfigDir, ".claude.json"))

	fakeDir := fakeClaudePath(t)
	holdFile := filepath.Join(t.TempDir(), "hold")
	if err := os.WriteFile(holdFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	// Session 1: stays alive.
	first, _, firstErr := launchCmd(env, fakeDir, holdFile, "--force", "launch", "work")
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Remove(holdFile)
		_ = first.Wait()
	}()

	// Wait until session 1 is running claude in the isolate.
	isolate := filepath.Join(env.ClaudeorchHome, "isolate", "work")
	deadline := time.Now().Add(10 * time.Second)
	for {
		if b, _ := os.ReadFile(env.StoreFile()); strings.Contains(string(b), `"isolated"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("session 1 never became isolated; stderr: %s", firstErr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond) // let the exec into fake claude settle

	// Session 2: same profile, different folder, NO --force, no hold.
	otherDir := t.TempDir()
	second, out, errOut := launchCmd(env, fakeDir, "", "launch", "--cwd", otherDir, "work", "--", "--resume", "x")
	if err := second.Run(); err != nil {
		t.Fatalf("second launch of the same profile was refused: %v\nstderr: %s", err, errOut.String())
	}

	// It joined the same isolate, ran in the requested folder, and passed args on.
	if !strings.Contains(out.String(), "CLAUDE_CONFIG_DIR="+isolate) {
		t.Errorf("second launch did not use the shared isolate dir; stdout: %s", out.String())
	}
	if !strings.Contains(out.String(), "PWD="+otherDir) {
		t.Errorf("--cwd not honored; stdout: %s", out.String())
	}
	if !strings.Contains(out.String(), "ARGS: --resume x") {
		t.Errorf("claude args not passed; stdout: %s", out.String())
	}
	if !strings.Contains(errOut.String(), "joining") {
		t.Errorf("expected a 'joining' notice, stderr: %s", errOut.String())
	}
}

// claudeorch's own flags written after the profile name would silently go to
// claude. They must be rejected with a pointer to the right position, while
// anything after an explicit "--" still passes through untouched.
func TestLaunch_FlagAfterProfileNameIsRejected(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-1", "Acme")
	env.WriteCredentials("tok", "ref")
	env.Run("add", "work").AssertSuccess(t)

	fakeDir := fakeClaudePath(t)

	cmd, _, stderr := launchCmd(env, fakeDir, "", "launch", "work", "--force")
	if err := cmd.Run(); err == nil {
		t.Fatal("expected launch work --force to be rejected")
	}
	if !strings.Contains(stderr.String(), "comes after the profile name") {
		t.Errorf("expected misplaced-flag hint, got: %s", stderr.String())
	}

	// After "--" the flag belongs to claude and must not be rejected.
	cmd, stdout, stderr := launchCmd(env, fakeDir, "", "--force", "launch", "work", "--", "--force")
	if err := cmd.Run(); err != nil {
		t.Fatalf("flag after -- should pass through: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "ARGS: --force") {
		t.Errorf("claude did not receive --force; stdout: %s", stdout.String())
	}
}

func TestLaunch_CwdMustBeADirectory(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-1", "Acme")
	env.WriteCredentials("tok", "ref")
	env.Run("add", "work").AssertSuccess(t)

	r := env.Run("--force", "launch", "--cwd", filepath.Join(t.TempDir(), "missing"), "work")
	r.AssertError(t)
	r.AssertContains(t, "--cwd")
}
