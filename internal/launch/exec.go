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
// When apiKey is non-empty, ANTHROPIC_API_KEY is also set so Claude Code
// picks up the profile's API key in the isolated session.
// Flushes all deferred cleanup before calling Exec (defers don't run after Exec).
//
// On success, Exec never returns. On failure, the error is returned.
func Exec(claudePath, isolateDir string, extraArgs []string, apiKey string) error {
	if claudePath == "" {
		var err error
		claudePath, err = exec.LookPath("claude")
		if err != nil {
			return fmt.Errorf("launch.Exec: 'claude' not found in PATH: %w", err)
		}
	}

	// Inherit env, override CLAUDE_CONFIG_DIR (and ANTHROPIC_API_KEY for API key profiles).
	env := make([]string, 0, len(os.Environ())+2)
	for _, e := range os.Environ() {
		if len(e) >= 17 && e[:17] == "CLAUDE_CONFIG_DIR" {
			continue
		}
		if apiKey != "" && len(e) >= 18 && e[:18] == "ANTHROPIC_API_KEY=" {
			continue
		}
		env = append(env, e)
	}
	env = append(env, "CLAUDE_CONFIG_DIR="+isolateDir)
	if apiKey != "" {
		env = append(env, "ANTHROPIC_API_KEY="+apiKey)
	}

	argv := append([]string{"claude"}, extraArgs...)
	return syscall.Exec(claudePath, argv, env)
}
