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
	// Force flat-file credential mode — the spawned claudeorch binaries
	// inherit this env. Without it, commands like swap/add would touch the
	// developer's real macOS Keychain entry.
	os.Setenv("CLAUDEORCH_NO_KEYCHAIN", "1")

	// Build the claudeorch binary into a temp dir.
	tmp, err := os.MkdirTemp("", "claudeorch-inttest-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "mktemp: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmp)

	cliBin = filepath.Join(tmp, "claudeorch")
	cmd := exec.Command("go", "build", "-o", cliBin, "github.com/DhilipBinny/claudeorch/cmd/claudeorch")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "build claudeorch: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}
