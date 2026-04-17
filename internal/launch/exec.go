//go:build !windows

package launch

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// Exec replaces the current process with 'claude' using syscall.Exec.
// Sets CLAUDE_CONFIG_DIR to isolateDir so Claude Code uses the isolate.
// Flushes all deferred cleanup before calling Exec (defers don't run after Exec).
//
// On success, Exec never returns. On failure, the error is returned.
func Exec(claudePath, isolateDir string, extraArgs []string) error {
	if claudePath == "" {
		var err error
		claudePath, err = exec.LookPath("claude")
		if err != nil {
			return fmt.Errorf("launch.Exec: 'claude' not found in PATH: %w", err)
		}
	}

	// Inherit env, override CLAUDE_CONFIG_DIR.
	env := make([]string, 0, len(os.Environ())+1)
	for _, e := range os.Environ() {
		if len(e) >= 17 && e[:17] == "CLAUDE_CONFIG_DIR" {
			continue
		}
		env = append(env, e)
	}
	env = append(env, "CLAUDE_CONFIG_DIR="+isolateDir)

	argv := append([]string{"claude"}, extraArgs...)
	return syscall.Exec(claudePath, argv, env)
}
