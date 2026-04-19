//go:build linux

package creds

import (
	"fmt"
	"os"

	"github.com/DhilipBinny/claudeorch/internal/fsio"
)

// ReadLive reads Claude Code's current OAuth credentials from disk.
// On Linux: ~/.claude/.credentials.json (or $CLAUDE_CONFIG_DIR/.credentials.json).
func ReadLive(credsPath string) ([]byte, error) {
	data, err := os.ReadFile(credsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no credentials found at %s — are you logged in to Claude Code?", credsPath)
		}
		return nil, fmt.Errorf("read credentials %s: %w", credsPath, err)
	}
	return data, nil
}

// WriteLive writes OAuth credentials to the live location.
// On Linux: atomic write to the flat file.
func WriteLive(credsPath string, data []byte) error {
	return fsio.WriteFileAtomic(credsPath, data, 0o600)
}

// IsKeychainBased reports whether this platform stores live credentials
// in an OS-managed secret store (Keychain, credential manager) rather
// than a flat file. False on Linux.
func IsKeychainBased() bool {
	return false
}
