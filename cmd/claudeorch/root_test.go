package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestRootCommand_HasAllPersistentFlags asserts that every persistent flag
// promised in DESIGN.md §6 is wired on the root command. If any flag is
// removed or renamed, this test fails loudly — which is intentional, because
// persistent flags are a user-facing contract.
func TestRootCommand_HasAllPersistentFlags(t *testing.T) {
	root := newRootCmd()
	flags := root.PersistentFlags()

	want := []string{"debug", "json", "no-color", "force"}
	for _, name := range want {
		if flags.Lookup(name) == nil {
			t.Errorf("persistent flag %q missing on root command", name)
		}
	}
}

// TestRootCommand_VersionFlag confirms --version prints our version string
// using the ldflags-populated vars. The user will see exactly this text.
func TestRootCommand_VersionFlag(t *testing.T) {
	origV, origC, origD := Version, Commit, BuildDate
	Version, Commit, BuildDate = "9.9.9", "abc1234", "2026-01-01"
	t.Cleanup(func() { Version, Commit, BuildDate = origV, origC, origD })

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"--version"})

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error running --version: %v", err)
	}

	got := out.String()
	want := "claudeorch 9.9.9 (commit: abc1234, built: 2026-01-01)"
	if !strings.Contains(got, want) {
		t.Errorf("--version output = %q, want to contain %q", got, want)
	}
}

// TestNoColor covers every combination of the flag and NO_COLOR env var.
// The spec at no-color.org is explicit: NO_COLOR must be set to a non-empty
// string to activate. Empty string = unset for this purpose.
func TestNoColor(t *testing.T) {
	tests := []struct {
		name      string
		flag      bool
		envSet    bool
		envVal    string
		wantColor bool // true == NoColor() should return true (colors suppressed)
	}{
		{"default: flag off, env unset", false, false, "", false},
		{"flag on, env unset", true, false, "", true},
		{"flag off, env set to '1'", false, true, "1", true},
		{"flag off, env set to 'any-value'", false, true, "anything", true},
		{"flag off, env set to empty string", false, true, "", false}, // empty != set per spec
		{"flag on, env set to empty string", true, true, "", true},    // flag wins
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagNoColor = tt.flag
			t.Cleanup(func() { flagNoColor = false })

			if tt.envSet {
				t.Setenv("NO_COLOR", tt.envVal)
			} else {
				// Ensure cleared for this subtest. t.Setenv handles restore.
				t.Setenv("NO_COLOR", "")
			}

			if got := NoColor(); got != tt.wantColor {
				t.Errorf("NoColor() = %v, want %v", got, tt.wantColor)
			}
		})
	}
}

// TestDebug covers flag + CLAUDEORCH_DEBUG env var combinations.
// Unlike NO_COLOR, empty-string CLAUDEORCH_DEBUG does NOT activate — debug
// is a deliberate per-invocation toggle that should never activate by accident.
func TestDebug(t *testing.T) {
	tests := []struct {
		name   string
		flag   bool
		envSet bool
		envVal string
		want   bool
	}{
		{"default: flag off, env unset", false, false, "", false},
		{"flag on, env unset", true, false, "", true},
		{"flag off, env=1", false, true, "1", true},
		{"flag off, env=true", false, true, "true", true},
		{"flag off, env empty string", false, true, "", false}, // empty is NOT activation
		{"flag on, env empty", true, true, "", true},           // flag wins
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagDebug = tt.flag
			t.Cleanup(func() { flagDebug = false })

			if tt.envSet {
				t.Setenv("CLAUDEORCH_DEBUG", tt.envVal)
			} else {
				t.Setenv("CLAUDEORCH_DEBUG", "")
			}

			if got := Debug(); got != tt.want {
				t.Errorf("Debug() = %v, want %v", got, tt.want)
			}
		})
	}
}
