package swap

import (
	"os"
	"testing"
)

// TestMain forces flat-file credential mode. Without this, Run() on macOS
// takes the Keychain path and the tests read — and overwrite — the user's
// real Claude Code keychain entry, destroying their live login.
func TestMain(m *testing.M) {
	os.Setenv("CLAUDEORCH_NO_KEYCHAIN", "1")
	os.Exit(m.Run())
}
