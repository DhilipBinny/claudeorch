// Package schema provides narrow parsers for Claude Code's on-disk file
// formats: .credentials.json and .claude.json. These parsers extract only
// the fields claudeorch needs; all other fields are treated as opaque and
// never re-serialized, which prevents accidentally stripping unknown keys
// when Claude Code adds new fields in future versions.
package schema

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// maxFileSize is the maximum byte size we will parse. Files larger than this
// are treated as corrupted or adversarial input.
const maxFileSize = 10 << 20 // 10 MiB

// ErrSchemaIncompatible is returned when a required top-level key is absent
// or the structure doesn't match what we expect. Callers should surface this
// as a human-readable "schema may be incompatible" message rather than a
// low-level error.
var ErrSchemaIncompatible = errors.New("schema: file structure incompatible with this version of claudeorch")

// CredentialType distinguishes the authentication mechanism a profile uses.
type CredentialType int

const (
	// CredentialOAuth is the original OAuth 2.0 flow (personal subscription
	// via `claude /login`). Credentials contain claudeAiOauth with
	// accessToken, refreshToken, and expiresAt.
	CredentialOAuth CredentialType = iota

	// CredentialAPIKey is an API-key-based flow (e.g., API plan or
	// organisation-provisioned keys). The credential is a single
	// long-lived key string (sk-ant-...) with no refresh rotation.
	CredentialAPIKey
)

// Credentials holds the subset of credential data that claudeorch needs.
//
// The raw JSON blob (the full file) is preserved in Raw so callers can pass
// it through to the OAuth refresh path without re-serializing and accidentally
// dropping unknown keys.
type Credentials struct {
	// Type indicates whether this is an OAuth or API key credential.
	Type CredentialType

	// --- OAuth fields (populated when Type == CredentialOAuth) ---

	AccessToken  string    // claudeAiOauth.accessToken
	RefreshToken string    // claudeAiOauth.refreshToken
	ExpiresAt    time.Time // claudeAiOauth.expiresAt

	// ExpiresAtWasNumeric reports whether the original expiresAt field was a
	// numeric value (ms since epoch) rather than an RFC3339 string. Needed by
	// the refresh path so it can write back the same type Claude Code uses.
	ExpiresAtWasNumeric bool

	// --- API key fields (populated when Type == CredentialAPIKey) ---

	// APIKey is the raw API key string (sk-ant-...).
	APIKey string

	// Raw is the full original JSON blob, suitable for opaque passthrough to
	// the refresh client which must preserve unknown fields. For API key
	// credentials, this is a JSON envelope: {"apiKey": "sk-ant-..."}.
	Raw []byte
}

// ParseCredentials extracts credential fields from a stored blob.
//
// Supports two formats:
//
//  1. OAuth: JSON with "claudeAiOauth" containing "accessToken",
//     "refreshToken", and "expiresAt". Returns Type == CredentialOAuth.
//
//  2. API key: JSON with "apiKey" containing the raw key string
//     (e.g., "sk-ant-..."). Returns Type == CredentialAPIKey.
//
// The blob must be ≤ maxFileSize and valid JSON.
func ParseCredentials(data []byte) (*Credentials, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty credentials file", ErrSchemaIncompatible)
	}
	if len(data) > maxFileSize {
		return nil, fmt.Errorf("schema: credentials file is %d bytes, exceeds max %d", len(data), maxFileSize)
	}

	// Try both formats from the same unmarshal.
	var envelope struct {
		ClaudeAiOauth *struct {
			AccessToken  string          `json:"accessToken"`
			RefreshToken string          `json:"refreshToken"`
			ExpiresAt    json.RawMessage `json:"expiresAt"`
		} `json:"claudeAiOauth"`
		APIKey string `json:"apiKey"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("schema: credentials JSON parse error: %w", err)
	}

	// API key format.
	if envelope.APIKey != "" {
		return &Credentials{
			Type:   CredentialAPIKey,
			APIKey: envelope.APIKey,
			Raw:    data,
		}, nil
	}

	// OAuth format.
	if envelope.ClaudeAiOauth == nil {
		return nil, fmt.Errorf("%w: missing \"claudeAiOauth\" and \"apiKey\" keys", ErrSchemaIncompatible)
	}
	oauth := envelope.ClaudeAiOauth
	if oauth.AccessToken == "" {
		return nil, fmt.Errorf("%w: empty accessToken (not logged in or corrupted)", ErrSchemaIncompatible)
	}
	if oauth.RefreshToken == "" {
		return nil, fmt.Errorf("%w: empty refreshToken (not logged in or corrupted)", ErrSchemaIncompatible)
	}

	expiresAt, numeric := parseExpiresAt(oauth.ExpiresAt)

	return &Credentials{
		Type:                CredentialOAuth,
		AccessToken:         oauth.AccessToken,
		RefreshToken:        oauth.RefreshToken,
		ExpiresAt:           expiresAt,
		ExpiresAtWasNumeric: numeric,
		Raw:                 data,
	}, nil
}

// MakeAPIKeyCredentialBlob creates the JSON blob for storing an API key
// in claudeorch's profile credentials.json format.
func MakeAPIKeyCredentialBlob(apiKey string) ([]byte, error) {
	blob := map[string]string{"apiKey": apiKey}
	return json.Marshal(blob)
}

// parseExpiresAt accepts either a JSON number (milliseconds since epoch) or
// a JSON string (RFC3339) and returns the parsed time plus a flag indicating
// which shape was read. Unparseable or absent values yield a zero time.
func parseExpiresAt(raw json.RawMessage) (t time.Time, numeric bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return time.Time{}, false
	}
	// Numeric branch: starts with a digit or minus sign.
	if raw[0] != '"' {
		var n int64
		if err := json.Unmarshal(raw, &n); err != nil {
			return time.Time{}, true
		}
		return time.UnixMilli(n).UTC(), true
	}
	// String branch.
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return time.Time{}, false
	}
	if s == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, false
}
