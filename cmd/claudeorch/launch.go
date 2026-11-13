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

// ownFlags are the claudeorch flags that claude does not itself accept.
// Because launch stops flag parsing at the profile name (so claude's flags
// pass through), one of these written after the name would silently go to
// claude instead of taking effect. Flags that claude (or one of its
// subcommands) also defines, such as --debug, --json and --cwd, are left alone
// since they may be meant for claude.
var ownFlags = []string{"--force", "--isolated"}

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

// splitClaudeArgs prepares the arguments that follow the profile name.
//
// A "--" written right after the name only marks where claudeorch's concern
// ends; forwarding it would end claude's own option parsing and turn the
// flags after it into prompt text, so it is dropped. A "--" anywhere later
// belongs to claude and is passed through untouched. It also returns the
// part before the first "--", the only part that can hold a misplaced
// claudeorch flag.
func splitClaudeArgs(args []string) (forClaude, toCheck []string) {
	toCheck = args
	for i, a := range args {
		if a == "--" {
			toCheck = args[:i]
			break
		}
	}
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	return args, toCheck
}

func runLaunch(cmd *cobra.Command, isolated bool, cwd string, args []string) error {
	name := args[0]
	var claudeArgs []string
	if len(args) > 1 {
		claudeArgs = args[1:]
	}

	// Flag parsing stops at the profile name, so cobra never sees a "--"
	// written right after it and would forward it to claude as a literal
	// argument. See splitClaudeArgs for what is consumed and checked.
	claudeArgs, unchecked := splitClaudeArgs(claudeArgs)
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
	rep, err := reconcileProfiles(store, cmd.ErrOrStderr())
	if err != nil {
		return fmt.Errorf("reconcile: %w", err)
	}

	// Refuse double-book: profile is already in another location.
	// Error messages are deliberately verbose — these are paths users hit
	// while learning the live-vs-isolate distinction, and terse messages
	// lead to "what does this mean" confusion.
	switch p.Location {
	case profile.LocationLive:
		if !flagForce {
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
		fmt.Fprintf(cmd.ErrOrStderr(),
			"Warning: launching %q while it's also live in ~/.claude/ — one side will break.\n", name)
	case profile.LocationIsolated:
		// Another claude already owns this profile's isolate dir (reconcile
		// above downgraded it if that process was dead). That's fine: all
		// launches of one profile share the same isolate dir and so the same
		// credentials file, exactly like several plain 'claude' sessions
		// share ~/.claude. Materialize won't overwrite the running
		// session's tokens or state, so just join it.
		//
		// The exception is a profile that is ALSO live in ~/.claude/: reconcile
		// keeps it "isolated" while a claude owns the isolate dir, so the
		// LocationLive refusal above never runs, yet the same refresh token
		// would be held twice and Materialize could copy the live copy in.
		if !flagForce && containsName(rep.IsolatedLiveConflicts, name) {
			return fmt.Errorf(
				"profile %q is running in an isolate session AND is also live in ~/.claude/.\n"+
					"\n"+
					"Joining would hold the same refresh token in two places; whichever side\n"+
					"refreshes first invalidates the other.\n"+
					"\n"+
					"Swap ~/.claude/ to a different profile first:\n"+
					"  claudeorch swap <other-profile>\n"+
					"\n"+
					"Or force it (you accept the 401 risk):\n"+
					"  claudeorch --force launch %s",
				name, name)
		}
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

	fmt.Fprintf(cmd.ErrOrStderr(), "Launching claude with profile %q (CLAUDE_CONFIG_DIR=%s)\n",
		name, isolateDir)

	if cwd != "" {
		if err := os.Chdir(cwd); err != nil {
			return fmt.Errorf("--cwd %q: %w", cwd, err)
		}
	}

	// Exec replaces this process — defers above have already run.
	return launch.Exec("", isolateDir, claudeArgs)
}

func containsName(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}
