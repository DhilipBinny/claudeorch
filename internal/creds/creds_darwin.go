//go:build darwin

package creds

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strings"

	"github.com/DhilipBinny/claudeorch/internal/fsio"
)

// keychainService is the exact service name Claude Code registers its OAuth
// credentials under in the macOS Keychain. Verified against a real macOS
// machine (2026-04-19): `security dump-keychain | grep -A5 claude` shows
// svce="Claude Code-credentials". This is NOT "claude.ai", "Claude Code",
// or "claude" — all of which we tested and got empty results for.
const keychainService = "Claude Code-credentials"

// ReadLive reads Claude Code's current OAuth credentials.
//
// On macOS, credentials live in the system Keychain, not on disk. Claude
// Code actively deletes any .credentials.json file it finds (issue #1414).
//
// Strategy:
//  1. Try the Keychain (the macOS-native path). Requires the login keychain
//     to be unlocked, which it always is for interactive GUI sessions. Over
//     SSH the caller may need to unlock first (`security unlock-keychain`).
//  2. Fall back to the flat file at credsPath. This handles CLAUDE_CONFIG_DIR
//     overrides and headless setups where the user placed creds manually.
//  3. If both fail, return a descriptive error with recovery steps.
func ReadLive(credsPath string) ([]byte, error) {
	// Try Keychain first.
	data, err := readKeychain()
	if err == nil && len(data) > 0 {
		return data, nil
	}

	// Fallback: flat file (CLAUDE_CONFIG_DIR, headless, or custom setup).
	fileData, fileErr := os.ReadFile(credsPath)
	if fileErr == nil && len(fileData) > 0 {
		return fileData, nil
	}

	return nil, fmt.Errorf(
		"no credentials found — tried macOS Keychain (service %q)\n"+
			"and flat file at %s.\n\n"+
			"Are you logged in to Claude Code? Run 'claude /login' first.\n\n"+
			"If you're in an SSH session, the Keychain may be locked. Unlock with:\n"+
			"  security unlock-keychain ~/Library/Keychains/login.keychain-db\n\n"+
			"See: https://github.com/anthropics/claude-code/issues/29816",
		keychainService, credsPath)
}

// WriteLive writes OAuth credentials to the live location.
//
// On macOS: writes to the Keychain (updating the existing entry). Also
// writes the flat file as fallback (Claude Code may delete it, but
// CLAUDE_CONFIG_DIR setups depend on it).
//
// If the Keychain write fails (locked, headless), the flat-file write
// still proceeds. If the flat-file write fails, the Keychain write
// already happened. Either one is sufficient for Claude Code to function.
func WriteLive(credsPath string, data []byte) error {
	keychainErr := writeKeychain(data)
	fileErr := fsio.WriteFileAtomic(credsPath, data, 0o600)

	if keychainErr != nil && fileErr != nil {
		return fmt.Errorf(
			"could not write credentials to Keychain (%v) or file (%v)\n\n"+
				"If in SSH, try: security unlock-keychain ~/Library/Keychains/login.keychain-db",
			keychainErr, fileErr)
	}
	return nil
}

// IsKeychainBased reports whether this platform stores live credentials
// in an OS-managed secret store. True on macOS.
func IsKeychainBased() bool {
	return true
}

// currentUsername returns the macOS username for the Keychain account field.
// Claude Code uses the OS username, not the Anthropic email.
func currentUsername() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "unknown"
}

// readKeychain reads the password field of the Claude Code credentials
// entry from the macOS Keychain. Returns (blob, nil) on success.
// The blob is the same JSON format as Linux's .credentials.json.
func readKeychain() ([]byte, error) {
	cmd := exec.Command("security", "find-generic-password",
		"-s", keychainService,
		"-a", currentUsername(),
		"-w",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		return nil, fmt.Errorf("keychain read: %s", errMsg)
	}
	return bytes.TrimSpace(stdout.Bytes()), nil
}

// writeKeychain writes (or updates) the Claude Code credentials entry in
// the macOS Keychain. Uses -U to update if the entry already exists.
func writeKeychain(data []byte) error {
	cmd := exec.Command("security", "add-generic-password",
		"-U",
		"-s", keychainService,
		"-a", currentUsername(),
		"-w", string(data),
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		return fmt.Errorf("keychain write: %s", errMsg)
	}
	return nil
}
