//go:build linux

package creds

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

// credentialsPath returns the default credentials file path on Linux.
func credentialsPath() string {
	home := os.Getenv("HOME")
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, ".credentials.json")
	}
	return filepath.Join(home, ".claude", ".credentials.json")
}

// ReadLiveAPIKey reads the API key from the credentials flat file on Linux.
// Claude Code on Linux stores API keys in ~/.claude/.credentials.json as
// {"apiKey": "sk-ant-..."}.
func ReadLiveAPIKey() (string, error) {
	data, err := os.ReadFile(credentialsPath())
	if err != nil {
		return "", fmt.Errorf("read credentials: %w", err)
	}
	var envelope struct {
		APIKey string `json:"apiKey"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return "", fmt.Errorf("parse credentials: %w", err)
	}
	key := strings.TrimSpace(envelope.APIKey)
	if key == "" {
		return "", fmt.Errorf("no apiKey found in credentials file")
	}
	return key, nil
}

// WriteLiveAPIKey writes the API key to the credentials flat file on Linux.
func WriteLiveAPIKey(apiKey string) error {
	blob, err := json.Marshal(map[string]string{"apiKey": apiKey})
	if err != nil {
		return fmt.Errorf("marshal API key: %w", err)
	}
	return fsio.WriteFileAtomic(credentialsPath(), blob, 0o600)
}

// DeleteLiveCredentials is a no-op on Linux (no Keychain to clear).
func DeleteLiveCredentials() error {
	return nil
}

// DeleteLiveAPIKey is a no-op on Linux (no Keychain to clear).
func DeleteLiveAPIKey() error {
	return nil
}
