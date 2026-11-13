//go:build linux

package reconcile

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func writeSessionFile(t *testing.T, isolate string, pid int) {
	t.Helper()
	dir := filepath.Join(isolate, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"pid":%d}`, pid)
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.json", pid)), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestIsolateHasLiveOwner_LiveSessionFile(t *testing.T) {
	iso := t.TempDir()
	writeSessionFile(t, iso, os.Getpid())
	if !isolateHasLiveOwner(iso) {
		t.Error("a live PID in sessions/ should count as an owner")
	}
}

func TestIsolateHasLiveOwner_NobodyHome(t *testing.T) {
	iso := t.TempDir()
	if isolateHasLiveOwner(iso) {
		t.Error("an empty isolate should have no owner")
	}
	writeSessionFile(t, iso, 2147483646) // stale
	if isolateHasLiveOwner(iso) {
		t.Error("only a stale session file should not count as an owner")
	}
}

// One stale session file must not hide a process that really runs in the
// isolate: the /proc signal still has to be consulted.
func TestIsolateHasLiveOwner_StaleFileDoesNotHideProcOwner(t *testing.T) {
	iso := t.TempDir()
	writeSessionFile(t, iso, 2147483646) // stale

	cmd := exec.Command("sleep", "30")
	cmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+iso)
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start helper process: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	if !isolateHasLiveOwner(iso) {
		t.Error("a process running with CLAUDE_CONFIG_DIR=isolate should count as an owner despite a stale session file")
	}
}
