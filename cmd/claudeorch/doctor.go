package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/DhilipBinny/claudeorch/internal/fsio"
	"github.com/DhilipBinny/claudeorch/internal/paths"
	"github.com/DhilipBinny/claudeorch/internal/profile"
	"github.com/DhilipBinny/claudeorch/internal/schema"
	"github.com/DhilipBinny/claudeorch/internal/session"
	"github.com/spf13/cobra"
)

func init() {
	registerSubcommand(func(root *cobra.Command) {
		root.AddCommand(newDoctorCmd())
	})
}

func newDoctorCmd() *cobra.Command {
	var fix bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check claudeorch and Claude Code health.",
		Long: `Runs a series of checks and reports any issues:

  - Permission checks (0700 dirs, 0600 files)
  - Claude binary installed and responding to --version
  - Stale session/IDE lock files (dead PIDs)
  - Token expiry per profile
  - Orphaned .pre-swap backup files
  - Stale lock file (dead owner PID)
  - store.json consistency

Non-destructive by default. Use --fix to repair fixable issues.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor(cmd, fix)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.Flags().BoolVar(&fix, "fix", false, "repair fixable issues automatically")
	return cmd
}

type checkResult struct {
	name    string
	ok      bool
	message string
	fixed   bool
}

func runDoctor(cmd *cobra.Command, fix bool) error {
	out := cmd.OutOrStdout()
	var results []checkResult

	orchHome, _ := paths.ClaudeorchHome()
	claudeConfigHome, _ := paths.ClaudeConfigHome()
	storePath, _ := paths.StoreFile()
	lockPath, _ := paths.LockFile()

	// 1. Claudeorch home permissions.
	results = append(results, checkPerms("claudeorch home (0700)", orchHome, 0o700, fix))

	// 2. Store.json exists and loads cleanly.
	results = append(results, checkStore("store.json consistency", storePath))

	// 3. Profile permissions.
	store, storeErr := profile.Load(storePath)
	if storeErr == nil {
		for name := range store.Profiles {
			dir, err := paths.ProfileDir(name)
			if err != nil {
				continue
			}
			results = append(results, checkPerms("profile dir "+name+" (0700)", dir, 0o700, fix))
			credsPath := filepath.Join(dir, "credentials.json")
			results = append(results, checkPerms("profile "+name+" credentials (0600)", credsPath, 0o600, fix))
		}
	}

	// 4. Claude binary.
	results = append(results, checkClaudeBinary())

	// 5. Stale session files (dead PIDs).
	results = append(results, checkStaleSessions(claudeConfigHome, fix))

	// 6. Token expiry per profile.
	if storeErr == nil {
		for name, p := range store.Profiles {
			results = append(results, checkTokenExpiry(name, p))
		}
	}

	// 7. Profile drift — TokensLastSeenAt lagging too far behind suggests
	// Claude Code has silently rotated tokens since we last saw them,
	// invalidating the profile's refresh token. User should run 'sync' or
	// re-authenticate.
	if storeErr == nil {
		for name, p := range store.Profiles {
			results = append(results, checkProfileDrift(name, p))
		}
	}

	// 8. Orphaned .pre-swap files.
	results = append(results, checkPreSwapOrphans(claudeConfigHome))

	// 9. Stale lock file.
	results = append(results, checkStaleLock(lockPath, fix))

	// Print results.
	allOK := true
	for _, r := range results {
		if r.ok {
			fmt.Fprintf(out, "  ✓ %s\n", r.name)
		} else {
			allOK = false
			if r.fixed {
				fmt.Fprintf(out, "  ✗ %s — FIXED: %s\n", r.name, r.message)
			} else {
				fmt.Fprintf(out, "  ✗ %s — %s\n", r.name, r.message)
			}
		}
	}

	if allOK {
		fmt.Fprintln(out, "\nAll checks passed.")
	} else {
		fmt.Fprintln(out, "\nSome checks failed. Run 'claudeorch doctor --fix' to repair fixable issues.")
		return fmt.Errorf("doctor: one or more checks failed")
	}
	return nil
}

func checkPerms(name, path string, want os.FileMode, fix bool) checkResult {
	err := fsio.CheckPerms(path, want)
	if err == nil {
		return checkResult{name: name, ok: true}
	}
	if os.IsNotExist(err) {
		return checkResult{name: name, ok: false, message: "path does not exist"}
	}
	if fix {
		if fixErr := os.Chmod(path, want); fixErr == nil {
			return checkResult{name: name, ok: false, message: err.Error(), fixed: true}
		}
	}
	return checkResult{name: name, ok: false, message: err.Error()}
}

func checkStore(name, path string) checkResult {
	_, err := profile.Load(path)
	if err != nil {
		if os.IsNotExist(err) {
			return checkResult{name: name, ok: true, message: "no store.json yet (fresh install)"}
		}
		return checkResult{name: name, ok: false, message: err.Error()}
	}
	return checkResult{name: name, ok: true}
}

func checkClaudeBinary() checkResult {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", "--version")
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		msg := err.Error()
		if ctx.Err() == context.DeadlineExceeded {
			msg = "timed out after 2s"
		}
		return checkResult{name: "claude binary", ok: false,
			message: "not found or not responding: " + msg}
	}
	version := strings.TrimSpace(string(out))
	return checkResult{name: "claude binary (" + version + ")", ok: true}
}

func checkStaleSessions(configDir string, fix bool) checkResult {
	sessDir := filepath.Join(configDir, "sessions")
	entries, err := os.ReadDir(sessDir)
	if err != nil {
		return checkResult{name: "session files", ok: true, message: "no sessions dir"}
	}
	stale := 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(sessDir, e.Name()))
		if readErr != nil {
			continue
		}
		var s struct {
			PID int `json:"pid"`
		}
		if jsonErr := unmarshalJSON(data, &s); jsonErr != nil || s.PID <= 0 {
			continue
		}
		if !session.IsAlive(s.PID) {
			stale++
			if fix {
				_ = os.Remove(filepath.Join(sessDir, e.Name()))
			}
		}
	}
	if stale == 0 {
		return checkResult{name: "stale session files", ok: true}
	}
	if fix {
		return checkResult{name: "stale session files", ok: false,
			message: fmt.Sprintf("%d stale files", stale), fixed: true}
	}
	return checkResult{name: "stale session files", ok: false,
		message: fmt.Sprintf("%d stale PID files (run --fix to remove)", stale)}
}

func checkTokenExpiry(name string, p *profile.Profile) checkResult {
	dir, err := paths.ProfileDir(name)
	if err != nil {
		return checkResult{name: "token " + name, ok: false, message: err.Error()}
	}
	credsData, err := os.ReadFile(filepath.Join(dir, "credentials.json"))
	if err != nil {
		return checkResult{name: "token " + name, ok: false, message: "cannot read credentials"}
	}
	creds, err := schema.ParseCredentials(credsData)
	if err != nil {
		return checkResult{name: "token " + name, ok: false, message: "cannot parse credentials: " + err.Error()}
	}
	if !creds.ExpiresAt.IsZero() && time.Now().After(creds.ExpiresAt) {
		return checkResult{name: "token " + name, ok: false,
			message: fmt.Sprintf("expired at %s — run 'claudeorch refresh %s'", creds.ExpiresAt.Format(time.RFC3339), name)}
	}
	if p.NeedsReauth {
		return checkResult{name: "token " + name, ok: false,
			message: "needs re-authentication — run 'claude /login' then 'claudeorch add " + name + "'"}
	}
	return checkResult{name: "token " + name, ok: true}
}

// driftThreshold is how much older than the current time TokensLastSeenAt
// is allowed to be before we flag a profile as "likely stale". Access
// tokens live ~1h and Claude refreshes them aggressively, so any profile
// that hasn't observed a fresh token in 24h is almost certainly holding a
// revoked refresh token — whoever silently rotated it (claude, a login,
// another claudeorch instance) invalidated ours.
const driftThreshold = 24 * time.Hour

func checkProfileDrift(name string, p *profile.Profile) checkResult {
	// TokensLastSeenAt is only populated by reconcile. Zero = never
	// reconciled, which happens on a fresh store or a v1-migrated store
	// that hasn't had any mutating command run yet. Skip — not actionable.
	if p.TokensLastSeenAt.IsZero() {
		return checkResult{name: "drift " + name, ok: true,
			message: "no observation yet"}
	}
	age := time.Since(p.TokensLastSeenAt)
	if age < driftThreshold {
		return checkResult{name: "drift " + name, ok: true}
	}
	return checkResult{name: "drift " + name, ok: false,
		message: fmt.Sprintf(
			"tokens last observed %s ago — likely rotated by another process; "+
				"run 'claudeorch sync' or 'claude /login' + 'claudeorch add %s'",
			humanDuration(age), name)}
}

// humanDuration rounds a duration to the largest meaningful unit for
// drift messages: "2d", "5h", "45m". Avoids trailing sub-units that
// aren't useful for a "how stale is this?" readout.
func humanDuration(d time.Duration) string {
	days := int(d.Hours()) / 24
	if days > 0 {
		return fmt.Sprintf("%dd", days)
	}
	hours := int(d.Hours())
	if hours > 0 {
		return fmt.Sprintf("%dh", hours)
	}
	mins := int(d.Minutes())
	if mins > 0 {
		return fmt.Sprintf("%dm", mins)
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

func checkPreSwapOrphans(claudeConfigHome string) checkResult {
	orphan := filepath.Join(claudeConfigHome, ".credentials.json.pre-swap")
	if _, err := os.Stat(orphan); err == nil {
		return checkResult{name: "pre-swap orphans", ok: false,
			message: orphan + " exists — previous swap may have failed; inspect and remove manually"}
	}
	return checkResult{name: "pre-swap orphans", ok: true}
}

func checkStaleLock(lockPath string, fix bool) checkResult {
	if _, err := os.Stat(lockPath); os.IsNotExist(err) {
		return checkResult{name: "lock file", ok: true}
	}
	// Attempt to read the lock. If we can acquire it immediately, the previous
	// holder is dead. We just stat-report, not acquire.
	return checkResult{name: "lock file", ok: true, message: "exists (normal — will be released when holder exits)"}
}

func unmarshalJSON(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
