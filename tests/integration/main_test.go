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
