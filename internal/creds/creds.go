// Package creds provides a platform-aware interface for reading and writing
// Claude Code's "live" OAuth credentials — the ones Claude itself actively
// uses and rotates.
//
// # Why this package exists
//
// On Linux, Claude Code stores credentials at ~/.claude/.credentials.json
// as a plain JSON file. On macOS, they're in the system Keychain under
// service name "Claude Code-credentials" (account = macOS username), and
// Claude Code actively deletes any .credentials.json file it finds on disk
// (see github.com/anthropics/claude-code/issues/1414).
//
// claudeorch needs to read these credentials on 'add' (snapshot the current
// account), compare them during 'reconcile' (detect drift), and write them
// on 'swap' (switch the active account) and 'refresh' (rotate tokens).
// This package hides the platform difference behind ReadLive/WriteLive so
// callers don't need build-tag spaghetti.
//
// Platform implementations:
//
//	creds_linux.go  — reads/writes ~/.claude/.credentials.json via fsio
//	creds_darwin.go — reads/writes macOS Keychain via the 'security' CLI
//
// Keychain details (verified against a real macOS machine, 2026-04-19):
//
//	Service:  "Claude Code-credentials"
//	Account:  macOS username (e.g. "binny")
//	Format:   identical JSON blob to Linux's .credentials.json
//	Keychain: ~/Library/Keychains/login.keychain-db
package creds

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/DhilipBinny/claudeorch/internal/paths"
)

// APIKeySource names where a live API key was discovered, so callers can
// tell the user which credential was saved.
type APIKeySource string

const (
	// APIKeySourceKeyring is the OS secret store (Keychain / Secret Service).
	APIKeySourceKeyring APIKeySource = "keyring"

	// APIKeySourceFile is .credentials.json on disk.
	APIKeySourceFile APIKeySource = "credentials file"

	// APIKeySourceEnv is the ANTHROPIC_API_KEY environment variable.
	APIKeySourceEnv APIKeySource = "ANTHROPIC_API_KEY environment variable"

	// APIKeySourceClaudeJSON is the primaryApiKey field of .claude.json,
	// which Claude Code provisions and updates when a Console account logs
	// in without a subscription.
	APIKeySourceClaudeJSON APIKeySource = ".claude.json primaryApiKey"
)

// apiKeyApproval is Claude Code's recorded verdict on a candidate API key.
type apiKeyApproval int

const (
	approvalUnknown apiKeyApproval = iota
	approvalApproved
	approvalRejected
)

// claudeJSONAuth is the subset of .claude.json that describes API-key auth.
type claudeJSONAuth struct {
	// PrimaryAPIKey is the key Claude Code itself is using.
	PrimaryAPIKey string `json:"primaryApiKey"`

	// CustomAPIKeyResponses records the user's answer to the "detected a
	// custom API key in your environment — use it?" prompt. Claude Code
	// stores only a trailing fragment of each key, never the whole thing.
	CustomAPIKeyResponses struct {
		Approved []string `json:"approved"`
		Rejected []string `json:"rejected"`
	} `json:"customApiKeyResponses"`
}

// readClaudeJSONAuth loads the auth-related subset of .claude.json.
// Best-effort: any failure yields a zero value, never an error worth
// surfacing, because every caller has other sources to try.
func readClaudeJSONAuth() claudeJSONAuth {
	var auth claudeJSONAuth
	path, err := paths.ClaudeJSONPath()
	if err != nil {
		return auth
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return auth
	}
	if err := json.Unmarshal(data, &auth); err != nil {
		return auth
	}
	auth.PrimaryAPIKey = strings.TrimSpace(auth.PrimaryAPIKey)
	return auth
}

// approvalFor reports Claude Code's verdict on key.
//
// customApiKeyResponses holds only a trailing fragment of each key, so the
// comparison is a suffix match. Rejection is checked first: if a key
// somehow appears in both lists, refusing to use it is the safe reading.
func (a claudeJSONAuth) approvalFor(key string) apiKeyApproval {
	if key == "" {
		return approvalUnknown
	}
	for _, frag := range a.CustomAPIKeyResponses.Rejected {
		if frag != "" && strings.HasSuffix(key, frag) {
			return approvalRejected
		}
	}
	for _, frag := range a.CustomAPIKeyResponses.Approved {
		if frag != "" && strings.HasSuffix(key, frag) {
			return approvalApproved
		}
	}
	return approvalUnknown
}

// apiKeyFromEnv returns the API key exported in the environment, if any.
func apiKeyFromEnv() string {
	return strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
}

// apiKeyFromClaudeJSON returns the primaryApiKey recorded in .claude.json.
func apiKeyFromClaudeJSON() string {
	return readClaudeJSONAuth().PrimaryAPIKey
}

// liveAPIKeyFromClaudeState resolves the API key Claude Code is actually
// authenticating with, for installs that keep no key in the keyring or in
// .credentials.json.
//
// Order matters, and it mirrors Claude Code's own precedence rather than
// simply preferring the environment:
//
//  1. ANTHROPIC_API_KEY when the user approved it. An approved environment
//     key overrides anything on disk, so it is the effective credential.
//  2. primaryApiKey from .claude.json. This is the key Claude Code
//     provisions at login, and it is what's in use whenever the
//     environment key was rejected or never offered.
//  3. ANTHROPIC_API_KEY with no recorded verdict, as a last resort.
//
// An environment key the user *rejected* is never returned. Saving it
// would hand the caller a credential Claude Code refuses to use — the
// exact trap that makes 'add' look like it worked and 'swap' produce a
// dead profile.
func liveAPIKeyFromClaudeState() (string, APIKeySource, bool) {
	auth := readClaudeJSONAuth()
	envKey := apiKeyFromEnv()
	envApproval := auth.approvalFor(envKey)

	if envKey != "" && envApproval == approvalApproved {
		return envKey, APIKeySourceEnv, true
	}
	if auth.PrimaryAPIKey != "" {
		return auth.PrimaryAPIKey, APIKeySourceClaudeJSON, true
	}
	if envKey != "" && envApproval != approvalRejected {
		return envKey, APIKeySourceEnv, true
	}
	return "", "", false
}

// EnvAPIKeyRejected reports whether an ANTHROPIC_API_KEY is exported but
// was explicitly declined by the user. Callers surface this so a stale key
// in a shell rc file does not look like the active credential.
func EnvAPIKeyRejected() bool {
	envKey := apiKeyFromEnv()
	if envKey == "" {
		return false
	}
	return readClaudeJSONAuth().approvalFor(envKey) == approvalRejected
}

// EnvAPIKeyShadowsProfiles reports whether an exported ANTHROPIC_API_KEY
// will override credentials claudeorch writes.
//
// True only when the key is exported and not rejected: Claude Code ignores
// a rejected environment key, so it shadows nothing. When true, 'swap'
// cannot change the effective account for new shells, though 'launch' still
// works because it overrides the variable for the process it execs (see
// internal/launch/exec.go).
func EnvAPIKeyShadowsProfiles() bool {
	envKey := apiKeyFromEnv()
	if envKey == "" {
		return false
	}
	return readClaudeJSONAuth().approvalFor(envKey) != approvalRejected
}
