//go:build windows

package launch

import "fmt"

// Exec is a stub for Windows (Phase 2).
func Exec(claudePath, isolateDir string, extraArgs []string) error {
	return fmt.Errorf("launch: exec not supported on Windows (Phase 2)")
}
