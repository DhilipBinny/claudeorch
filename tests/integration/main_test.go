//go:build integration

package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	// os.Exit skips defers, so run the real work in a helper and exit
	// with its code — otherwise the temp build dir leaks on every run.
	os.Exit(testMain(m))
}

func testMain(m *testing.M) int {
	// Force flat-file credential mode — the spawned claudeorch binaries
	// inherit this env. They are real (non-test) binaries, so the
	// testing.Testing() guard in internal/creds does not protect them;
	// without this env they would touch the developer's real macOS
	// Keychain entry on commands like swap/add.
	os.Setenv("CLAUDEORCH_NO_KEYCHAIN", "1")

	// Build the claudeorch binary into a temp dir.
	tmp, err := os.MkdirTemp("", "claudeorch-inttest-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "mktemp: %v\n", err)
		return 1
	}
	defer os.RemoveAll(tmp)

	cliBin = filepath.Join(tmp, "claudeorch")
	cmd := exec.Command("go", "build", "-o", cliBin, "github.com/DhilipBinny/claudeorch/cmd/claudeorch")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "build claudeorch: %v\n", err)
		return 1
	}

	return m.Run()
}
