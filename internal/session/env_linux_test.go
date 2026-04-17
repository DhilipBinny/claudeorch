//go:build linux

package session

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestConfigDirForPID_ReadsFromRealProcess launches a trivial subprocess with
// CLAUDE_CONFIG_DIR in its env and verifies we read it back from /proc.
func TestConfigDirForPID_ReadsFromRealProcess(t *testing.T) {
	want := "/tmp/claudeorch-env-test-dir"
	cmd := exec.Command("sleep", "5")
	cmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+want)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()
	// Give the kernel a moment to expose /proc/<pid>/environ.
	time.Sleep(20 * time.Millisecond)

	got := ConfigDirForPID(cmd.Process.Pid)
	if got != want {
		t.Errorf("ConfigDirForPID(%d) = %q, want %q", cmd.Process.Pid, got, want)
	}
}

func TestConfigDirForPID_UnsetEnv_ReturnsEmpty(t *testing.T) {
	// Spawn without CLAUDE_CONFIG_DIR in env.
	cmd := exec.Command("sleep", "5")
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()
	time.Sleep(20 * time.Millisecond)

	got := ConfigDirForPID(cmd.Process.Pid)
	if got != "" {
		t.Errorf("expected empty for unset env, got %q", got)
	}
}

func TestConfigDirForPID_DeadPID_ReturnsEmpty(t *testing.T) {
	if got := ConfigDirForPID(1<<31 - 1); got != "" {
		t.Errorf("expected empty for dead pid, got %q", got)
	}
}

func TestConfigDirForPID_InvalidPID_ReturnsEmpty(t *testing.T) {
	for _, pid := range []int{0, -1, -999} {
		if got := ConfigDirForPID(pid); got != "" {
			t.Errorf("ConfigDirForPID(%d) = %q, want empty", pid, got)
		}
	}
}
