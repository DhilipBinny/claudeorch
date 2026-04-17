package paths

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// Helper: set HOME and unset the override vars to a known state.
func setupEnv(t *testing.T, home, claudeConfigDir, claudeorchHome string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", claudeConfigDir)
	t.Setenv("CLAUDEORCH_HOME", claudeorchHome)
}

// Missing $HOME must return ErrNoHome — never a silent fallback.
func TestHome_Missing(t *testing.T) {
	setupEnv(t, "", "", "")

	tests := []struct {
		name string
		call func() (string, error)
	}{
		{"ClaudeConfigHome", ClaudeConfigHome},
		{"ClaudeJSONPath", ClaudeJSONPath},
		{"ClaudeCredentialsPath", ClaudeCredentialsPath},
		{"ClaudeSessionsDir", ClaudeSessionsDir},
		{"ClaudeIDEDir", ClaudeIDEDir},
		{"ClaudeorchHome", ClaudeorchHome},
		{"StoreFile", StoreFile},
		{"ProfilesRoot", ProfilesRoot},
		{"IsolatesRoot", IsolatesRoot},
		{"LockFile", LockFile},
		{"LogDir", LogDir},
		{"LogFile", LogFile},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.call()
			if !errors.Is(err, ErrNoHome) {
				t.Errorf("%s() err = %v, want ErrNoHome", tt.name, err)
			}
		})
	}
}

// CLAUDE_CONFIG_DIR unset → ~/.claude, .claude.json at $HOME.
// This is the default configuration for most users.
func TestClaudeConfigHome_Default(t *testing.T) {
	setupEnv(t, "/home/alice", "", "")

	got, err := ClaudeConfigHome()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	want := "/home/alice/.claude"
	if got != want {
		t.Errorf("ClaudeConfigHome() = %q, want %q", got, want)
	}
}

// CLAUDE_CONFIG_DIR set → use that value verbatim.
func TestClaudeConfigHome_Override(t *testing.T) {
	setupEnv(t, "/home/alice", "/custom/claude", "")

	got, err := ClaudeConfigHome()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	want := "/custom/claude"
	if got != want {
		t.Errorf("ClaudeConfigHome() = %q, want %q", got, want)
	}
}

// The .claude.json asymmetry is load-bearing. Two scenarios:
//   - CLAUDE_CONFIG_DIR unset → $HOME/.claude.json (at HOME, NOT inside .claude/)
//   - CLAUDE_CONFIG_DIR set → <CLAUDE_CONFIG_DIR>/.claude.json (inside the dir)
func TestClaudeJSONPath_Asymmetry(t *testing.T) {
	tests := []struct {
		name            string
		home            string
		claudeConfigDir string
		want            string
	}{
		{
			name:            "unset — lives at HOME (NOT inside .claude/)",
			home:            "/home/alice",
			claudeConfigDir: "",
			want:            "/home/alice/.claude.json",
		},
		{
			name:            "set — lives inside the override dir",
			home:            "/home/alice",
			claudeConfigDir: "/custom/claude",
			want:            "/custom/claude/.claude.json",
		},
		{
			name:            "set to unusual location — no special logic, just joined",
			home:            "/home/alice",
			claudeConfigDir: "/tmp/testrun",
			want:            "/tmp/testrun/.claude.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupEnv(t, tt.home, tt.claudeConfigDir, "")
			got, err := ClaudeJSONPath()
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != tt.want {
				t.Errorf("ClaudeJSONPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Regression guard: .claude.json at HOME must never resolve to .claude/.claude.json
// when CLAUDE_CONFIG_DIR is unset. That would break identity extraction.
func TestClaudeJSONPath_NeverInsideClaudeDirByDefault(t *testing.T) {
	setupEnv(t, "/home/alice", "", "")
	got, err := ClaudeJSONPath()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, ".claude/.claude.json") {
		t.Errorf("ClaudeJSONPath() = %q — must NOT be inside .claude/ when CLAUDE_CONFIG_DIR unset", got)
	}
}

// Credentials live inside config home, regardless of env-var state.
func TestClaudeCredentialsPath(t *testing.T) {
	tests := []struct {
		claudeConfigDir string
		want            string
	}{
		{"", "/home/alice/.claude/.credentials.json"},
		{"/elsewhere", "/elsewhere/.credentials.json"},
	}
	for _, tt := range tests {
		t.Run(tt.claudeConfigDir, func(t *testing.T) {
			setupEnv(t, "/home/alice", tt.claudeConfigDir, "")
			got, err := ClaudeCredentialsPath()
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClaudeSessionsAndIDEDirs(t *testing.T) {
	setupEnv(t, "/home/alice", "", "")

	sessions, err := ClaudeSessionsDir()
	if err != nil {
		t.Fatal(err)
	}
	if sessions != "/home/alice/.claude/sessions" {
		t.Errorf("ClaudeSessionsDir = %q", sessions)
	}

	ide, err := ClaudeIDEDir()
	if err != nil {
		t.Fatal(err)
	}
	if ide != "/home/alice/.claude/ide" {
		t.Errorf("ClaudeIDEDir = %q", ide)
	}
}

func TestClaudeorchHome_Default(t *testing.T) {
	setupEnv(t, "/home/alice", "", "")

	got, err := ClaudeorchHome()
	if err != nil {
		t.Fatal(err)
	}
	if got != "/home/alice/.claudeorch" {
		t.Errorf("got %q", got)
	}
}

func TestClaudeorchHome_Override(t *testing.T) {
	setupEnv(t, "/home/alice", "", "/tmp/test-ch")

	got, err := ClaudeorchHome()
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/test-ch" {
		t.Errorf("got %q", got)
	}
}

func TestClaudeorch_SubPaths(t *testing.T) {
	setupEnv(t, "/home/alice", "", "")

	tests := []struct {
		name string
		call func() (string, error)
		want string
	}{
		{"StoreFile", StoreFile, "/home/alice/.claudeorch/store.json"},
		{"ProfilesRoot", ProfilesRoot, "/home/alice/.claudeorch/profiles"},
		{"IsolatesRoot", IsolatesRoot, "/home/alice/.claudeorch/isolate"},
		{"LockFile", LockFile, "/home/alice/.claudeorch/locks/.lock"},
		{"LogDir", LogDir, "/home/alice/.claudeorch/log"},
		{"LogFile", LogFile, "/home/alice/.claudeorch/log/claudeorch.log"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.call()
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProfileDir_ValidName(t *testing.T) {
	setupEnv(t, "/home/alice", "", "")

	got, err := ProfileDir("work")
	if err != nil {
		t.Fatal(err)
	}
	want := "/home/alice/.claudeorch/profiles/work"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Path traversal attempts must be rejected cleanly.
func TestProfileDir_RejectsInvalidNames(t *testing.T) {
	setupEnv(t, "/home/alice", "", "")

	bad := []string{
		"",
		"..",
		".",
		"../etc",
		"work/sub",
		"work\\sub",
		".hidden",
		"has space",
		"has\ttab",
		"has\nnewline",
		"ends-with-slash/",
		"has:colon",
		"has@at",
		"has$dollar",
		string(make([]byte, 65)), // 65 chars, exceeds 64-char limit
	}
	for _, name := range bad {
		t.Run("bad-"+name, func(t *testing.T) {
			_, err := ProfileDir(name)
			if !errors.Is(err, ErrInvalidName) {
				t.Errorf("ProfileDir(%q) err = %v, want ErrInvalidName", name, err)
			}
		})
	}
}

func TestProfileDir_AcceptsValidNames(t *testing.T) {
	setupEnv(t, "/home/alice", "", "")

	good := []string{
		"work",
		"home",
		"a",                     // single char
		"alice-personal",        // hyphen
		"alice_work",            // underscore
		"ABC123",                // mixed case + digits
		"1profile",              // starts with digit (alphanumeric)
		strings.Repeat("a", 64), // max length
	}
	for _, name := range good {
		t.Run("good-"+name, func(t *testing.T) {
			got, err := ProfileDir(name)
			if err != nil {
				t.Errorf("ProfileDir(%q) unexpected err: %v", name, err)
				return
			}
			expected := filepath.Join("/home/alice/.claudeorch/profiles", name)
			if got != expected {
				t.Errorf("got %q, want %q", got, expected)
			}
		})
	}
}

func TestIsolateDir_ValidatesName(t *testing.T) {
	setupEnv(t, "/home/alice", "", "")

	// Reuses the same validator — quick positive + negative check.
	if _, err := IsolateDir("work"); err != nil {
		t.Errorf("IsolateDir(\"work\") unexpected err: %v", err)
	}
	if _, err := IsolateDir("../evil"); !errors.Is(err, ErrInvalidName) {
		t.Errorf("IsolateDir(\"../evil\") err = %v, want ErrInvalidName", err)
	}
}

func TestTmpSwapDir(t *testing.T) {
	setupEnv(t, "/home/alice", "", "")

	got, err := TmpSwapDir(12345)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/home/alice/.claudeorch/tmp-swap-12345" {
		t.Errorf("got %q", got)
	}
}

func TestTmpSwapDir_RejectsInvalidPID(t *testing.T) {
	setupEnv(t, "/home/alice", "", "")

	for _, pid := range []int{0, -1, -100} {
		if _, err := TmpSwapDir(pid); err == nil {
			t.Errorf("TmpSwapDir(%d) err = nil, want non-nil", pid)
		}
	}
}

func TestValidateProfileName_Standalone(t *testing.T) {
	// Exercise the public validator directly so callers can use it without
	// going through a path-constructing function.
	if err := ValidateProfileName("valid_name-1"); err != nil {
		t.Errorf("valid name rejected: %v", err)
	}
	if err := ValidateProfileName("invalid/name"); !errors.Is(err, ErrInvalidName) {
		t.Errorf("invalid name err = %v, want ErrInvalidName", err)
	}
}
