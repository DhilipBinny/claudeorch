//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// rawTokenPatterns are patterns that must NEVER appear in debug output.
// These are the same shapes that internal/log/redact.go targets.
var rawTokenPatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{20,}`),
	regexp.MustCompile(`sk-[A-Za-z0-9]{32,}`),
	regexp.MustCompile(`ref_[A-Za-z0-9_\-]{20,}`),
	regexp.MustCompile(`eyJ[A-Za-z0-9_=\-]{5,}\.[A-Za-z0-9_=\-]{5,}\.[A-Za-z0-9_=\-]{5,}`),
}

// syntheticAccessToken and syntheticRefreshToken are long enough to match the
// patterns above, so if redaction fails they will be caught.
const (
	syntheticAccessToken  = "sk-ant-oat01-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	syntheticRefreshToken = "ref_BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
)

func assertNoRawTokens(t *testing.T, label, text string) {
	t.Helper()
	for _, pat := range rawTokenPatterns {
		if pat.MatchString(text) {
			t.Errorf("raw token pattern %q found in %s output:\n%s", pat.String(), label, text)
		}
	}
}

func TestRedaction_DebugOutputContainsNoRawTokens(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials(syntheticAccessToken, syntheticRefreshToken)

	// Add a profile so subsequent commands have state to operate on.
	env.Run("add", "work").AssertSuccess(t)

	// Commands to exercise with --debug. We run them and check both stderr and
	// the on-disk log file for raw token patterns.
	cmds := [][]string{
		{"--debug", "list", "--no-usage"},
		{"--debug", "status"},
		{"--debug", "doctor"},
	}

	for _, args := range cmds {
		r := env.Run(args...)
		label := strings.Join(args, " ")

		assertNoRawTokens(t, label+" stderr", r.Stderr)
		assertNoRawTokens(t, label+" stdout", r.Stdout)
	}

	// Check the on-disk log file too if it was created.
	logPath := filepath.Join(env.ClaudeorchHome, "log", "claudeorch.log")
	if data, err := os.ReadFile(logPath); err == nil {
		assertNoRawTokens(t, "log file", string(data))
	}
}

func TestRedaction_AddCommandDebug(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("bob@example.com", "org-uuid-2", "BobCo")
	env.WriteCredentials(syntheticAccessToken, syntheticRefreshToken)

	r := env.Run("--debug", "add", "mywork")
	r.AssertSuccess(t)

	assertNoRawTokens(t, "add stderr", r.Stderr)
	assertNoRawTokens(t, "add stdout", r.Stdout)

	logPath := filepath.Join(env.ClaudeorchHome, "log", "claudeorch.log")
	if data, err := os.ReadFile(logPath); err == nil {
		assertNoRawTokens(t, "add log file", string(data))
	}
}
