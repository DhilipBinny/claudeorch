package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DhilipBinny/claudeorch/internal/fsio"
	"github.com/DhilipBinny/claudeorch/internal/launch"
	"github.com/DhilipBinny/claudeorch/internal/paths"
	"github.com/DhilipBinny/claudeorch/internal/profile"
	"github.com/DhilipBinny/claudeorch/internal/schema"
	"github.com/spf13/cobra"
)

func init() {
	registerSubcommand(func(root *cobra.Command) {
		root.AddCommand(newLaunchCmd())
	})
}

func newLaunchCmd() *cobra.Command {
	var isolated bool
	var cwd string
	cmd := &cobra.Command{
		Use:   "launch <name> [-- claude-args...]",
		Short: "Launch Claude Code with a specific profile in isolate mode.",
		Long: `Materializes an isolate directory for the named profile and execs
'claude' with CLAUDE_CONFIG_DIR pointing at it.

By default, shared content (CLAUDE.md, projects/, skills/, settings.json) is
symlinked from the default Claude config home. Use --isolated to skip all
symlinks for a fully independent session.

A profile can be launched any number of times, from any folder, just like
plain 'claude': every launch of the same profile shares one isolate dir (and
therefore one set of tokens). Use --cwd to start in a specific folder:
  claudeorch launch --cwd ~/code/app work

claudeorch's own flags (--force, --cwd, --isolated) must come BEFORE the
profile name; everything after the name is handed to claude. Pass extra
arguments to claude after --:
  claudeorch launch work -- --dangerously-skip-permissions`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLaunch(cmd, isolated, cwd, args)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
		// Disable flag parsing after first non-flag arg so -- pass-through works.
		TraverseChildren: false,
	}
	cmd.Flags().BoolVar(&isolated, "isolated", false, "no symlinks — fully independent session")
	cmd.Flags().StringVar(&cwd, "cwd", "", "start claude in this directory (default: current directory)")
	cmd.Flags().SetInterspersed(false) // stop parsing flags after first non-flag
	return cmd
}

// ownFlags are claudeorch's own flags. Because launch stops flag parsing at
// the profile name (so claude's flags pass through), one of these written
// after the name would silently go to claude instead of taking effect.
var ownFlags = []string{"--force", "--cwd", "--isolated", "--debug", "--json", "--no-color"}

// misplacedOwnFlag returns the first claudeorch flag found in args, matching
// both "--flag" and "--flag=value" forms.
func misplacedOwnFlag(args []string) string {
	for _, a := range args {
		for _, f := range ownFlags {
			if a == f || strings.HasPrefix(a, f+"=") {
				return f
			}
		}
	}
	return ""
}

func runLaunch(cmd *cobra.Command, isolated bool, cwd string, args []string) error {
	name := args[0]
	var claudeArgs []string
	if len(args) > 1 {
		claudeArgs = args[1:]
	}

	// Flag parsing stops at the profile name, so cobra never sees a "--"
	// written after it and would forward it to claude as a literal argument
	// (which ends claude's own option parsing and turns the flags after it
	// into prompt text). Consume the first "--" here instead: it only marks
	// where claudeorch's concern ends. Only the part before it can hold a
	// misplaced claudeorch flag; everything after is claude's verbatim.
	unchecked := claudeArgs
	for i, a := range claudeArgs {
		if a == "--" {
			unchecked = claudeArgs[:i]
			claudeArgs = append(append([]string{}, claudeArgs[:i]...), claudeArgs[i+1:]...)
			break
		}
	}
	if f := misplacedOwnFlag(unchecked); f != "" {
		return fmt.Errorf(
			"%s comes after the profile name, so it would be passed to claude instead of claudeorch.\n"+
				"Put it before the profile name:\n"+
				"  claudeorch launch %s %s ...\n"+
				"(for --force, which is a global flag: claudeorch --force launch %s)",
			f, f, name, name)
	}

	if cwd != "" {
		abs, err := filepath.Abs(cwd)
		if err != nil {
			return fmt.Errorf("--cwd %q: %w", cwd, err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return fmt.Errorf("--cwd %q: %w", cwd, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("--cwd %q is not a directory", cwd)
		}
		cwd = abs
	}

	if err := paths.ValidateProfileName(name); err != nil {
		return err
	}

	storePath, err := paths.StoreFile()
	if err != nil {
		return err
	}
	// Preliminary load for fast fail-on-missing-name — avoids acquiring the
	// lock just to tell the user they mistyped a profile name.
	store, err := profile.Load(storePath)
	if err != nil {
		return fmt.Errorf("load store: %w", err)
	}
	if _, ok := store.Profiles[name]; !ok {
		return fmt.Errorf("profile %q not found", name)
	}

	profileDir, err := paths.ProfileDir(name)
	if err != nil {
		return err
	}
	isolateDir, err := paths.IsolateDir(name)
	if err != nil {
		return err
	}
	claudeConfigHome, err := paths.ClaudeConfigHome()
	if err != nil {
		return err
	}

	// Ensure parent of isolate dir exists.
	if err := fsio.EnsureDir(filepath.Dir(isolateDir), 0o700); err != nil {
		return err
	}

	// Acquire the global lock. Reconcile + double-launch check + state
	// transitions all happen under lock. Materialize runs AFTER release
	// because it's per-profile (isolate/<name>/) and the lock protects
	// store.json, not per-profile dirs.
	lockPath, err := paths.LockFile()
	if err != nil {
		return err
	}
	if err := fsio.EnsureDir(filepath.Dir(lockPath), 0o700); err != nil {
		return err
	}
	release, err := fsio.AcquireLock(context.Background(), lockPath)
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	lockReleased := false
	defer func() {
		if !lockReleased {
			_ = release()
		}
	}()

	// Reload authoritative store under lock.
	store, err = profile.Load(storePath)
	if err != nil {
		return fmt.Errorf("load store: %w", err)
	}
	p, ok := store.Profiles[name]
	if !ok {
		return fmt.Errorf("profile %q not found", name)
	}

	// Freshest-wins: pull any newer tokens (from live if it matches, or
	// from a prior isolate session) back into profile/<name> before we
	// materialize. Without this, launch would overwrite fresh tokens in
	// isolate with stale tokens from profile — the v0.1.0 bug.
	if _, err := reconcileProfiles(store, cmd.ErrOrStderr()); err != nil {
		return fmt.Errorf("reconcile: %w", err)
	}

	// API key profiles don't suffer from OAuth token rotation conflicts,
	// so the double-book warnings are relaxed.
	isAPIKey := p.Source == profile.SourceAPIKey

	// Refuse double-book: profile is already in another location.
	// Error messages are deliberately verbose — these are paths users hit
	// while learning the live-vs-isolate distinction, and terse messages
	// lead to "what does this mean" confusion.
	switch p.Location {
	case profile.LocationLive:
		if !isAPIKey && !flagForce {
			return fmt.Errorf(
				"profile %q is your default account right now — its tokens live in ~/.claude/.\n"+
					"\n"+
					"Launch would put the same tokens in an isolate dir too. Both copies would fight\n"+
					"over OAuth refresh-token rotation: whichever side refreshes first invalidates\n"+
					"the other, and the loser gets 401 on its next API call.\n"+
					"\n"+
					"What you probably want:\n"+
					"\n"+
					"  • Just use %s now — no claudeorch needed, it's already the default:\n"+
					"      claude\n"+
					"\n"+
					"  • Run %s AND a different account side-by-side (the whole point of launch):\n"+
					"      claudeorch swap <other-profile>    # other account becomes default\n"+
					"      claudeorch launch %s                # safe now — %s goes into an isolate\n"+
					"\n"+
					"  • Force it anyway (you accept the 401 risk):\n"+
					"      claudeorch --force launch %s",
				name, name, name, name, name, name)
		}
		if !isAPIKey {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"Warning: launching %q while it's also live in ~/.claude/ — one side will break.\n", name)
		}
	case profile.LocationIsolated:
		// Another claude already owns this profile's isolate dir (reconcile
		// above downgraded it if that process was dead). That's fine: all
		// launches of one profile share the same isolate dir and so the same
		// credentials file, exactly like several plain 'claude' sessions
		// share ~/.claude. Materialize won't overwrite the running
		// session's tokens or state, so just join it.
		fmt.Fprintf(cmd.ErrOrStderr(),
			"Profile %q already has a running session; joining its isolate dir.\n", name)
	}

	// Mark isolated pre-emptively so concurrent launches see the state
	// change as soon as we save.
	p.Location = profile.LocationIsolated
	p.LastUsedAt = timeNow()
	if err := store.Save(storePath); err != nil {
		return fmt.Errorf("save store: %w", err)
	}

	// Release the lock BEFORE materialize + exec. Materialize writes only
	// to isolate/<name>/; the "isolated" marker we just saved prevents
	// other processes from racing on this profile.
	_ = release()
	lockReleased = true

	// Materialize the isolate directory (idempotent).
	if _, err := launch.Materialize(profileDir, isolateDir, claudeConfigHome, isolated); err != nil {
		return fmt.Errorf("materialize isolate: %w", err)
	}

	// For API key profiles, read the key to pass via environment variable.
	var apiKey string
	if isAPIKey {
		credsPath := filepath.Join(profileDir, "credentials.json")
		credsData, readErr := os.ReadFile(credsPath)
		if readErr == nil {
			parsed, parseErr := schema.ParseCredentials(credsData)
			if parseErr == nil && parsed.Type == schema.CredentialAPIKey {
				apiKey = parsed.APIKey
			}
		}
	}

	// If the isolate's own saved login already holds exactly this key, Claude
	// Code authenticates with it by itself. Also passing it through
	// ANTHROPIC_API_KEY only makes Claude Code print "ANTHROPIC_API_KEY
	// overriding saved Console key". Drop the redundant variable, and clear any
	// different key inherited from the shell so it cannot override the profile.
	// When the saved key is absent or differs, the explicit override stays.
	if apiKey != "" && launch.SavedAPIKeyMatches(isolateDir, apiKey) {
		_ = os.Unsetenv("ANTHROPIC_API_KEY")
		apiKey = ""
	}

	fmt.Fprintf(cmd.ErrOrStderr(), "Launching claude with profile %q (CLAUDE_CONFIG_DIR=%s)\n",
		name, isolateDir)

	if cwd != "" {
		if err := os.Chdir(cwd); err != nil {
			return fmt.Errorf("--cwd %q: %w", cwd, err)
		}
	}

	// Exec replaces this process — defers above have already run.
	return launch.Exec("", isolateDir, claudeArgs, apiKey)
}
