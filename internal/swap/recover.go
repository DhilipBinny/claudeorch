package swap

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/DhilipBinny/claudeorch/internal/session"
)

// Recover scans claudeorchHome for orphaned swap artifacts left by a crashed
// swap operation and cleans them up.
//
// Artifacts that may be orphaned:
//   - tmp-swap-<pid>/  — staging dirs from crashed pre-commit operations
//   - *.pre-swap       — backup files in claude config home
//
// A staging dir is cleaned if its PID is no longer alive.
// .pre-swap files are reported as warnings (they may indicate a half-swap).
//
// Recover is called from root PreRun before any state-mutating command.
func Recover(claudeorchHome, claudeConfigHome string) {
	recoverTmpDirs(claudeorchHome)
	reportPreSwapBackups(claudeConfigHome)
}

func recoverTmpDirs(claudeorchHome string) {
	entries, err := os.ReadDir(claudeorchHome)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "tmp-swap-") {
			continue
		}
		pidStr := strings.TrimPrefix(e.Name(), "tmp-swap-")
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}
		if session.IsAlive(pid) {
			continue
		}
		// PID is dead — remove orphan staging dir.
		dir := filepath.Join(claudeorchHome, e.Name())
		if removeErr := os.RemoveAll(dir); removeErr != nil {
			slog.Warn("swap recover: failed to remove orphan staging dir",
				"dir", dir, "err", removeErr)
		} else {
			slog.Info("swap recover: removed orphan staging dir", "dir", dir, "dead_pid", pid)
		}
	}
}

func reportPreSwapBackups(claudeConfigHome string) {
	for _, suffix := range []string{
		filepath.Join(claudeConfigHome, ".credentials.json.pre-swap"),
	} {
		if _, err := os.Stat(suffix); err == nil {
			slog.Warn("swap recover: orphaned .pre-swap backup found — previous swap may have been interrupted",
				"file", suffix,
				"hint", fmt.Sprintf("inspect %s and run 'claudeorch doctor --fix' to repair", suffix))
		}
	}
}
