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

// Credentials holds the subset of .credentials.json that claudeorch needs.
//
// The raw JSON blob (the full file) is preserved in Raw so callers can pass
// it through to the OAuth refresh path without re-serializing and accidentally
// dropping unknown keys.
type Credentials struct {
	AccessToken  string    // claudeAiOauth.accessToken
	RefreshToken string    // claudeAiOauth.refreshToken
	ExpiresAt    time.Time // claudeAiOauth.expiresAt (RFC3339 string in file)

	// Raw is the full original JSON blob, suitable for opaque passthrough to
	// the refresh client which must preserve unknown fields.
	Raw []byte
}

// ParseCredentials extracts the OAuth fields from a .credentials.json blob.
//
// The blob must:
//   - be ≤ maxFileSize
//   - be valid JSON
//   - contain "claudeAiOauth" with at minimum "accessToken", "refreshToken",
//     and "expiresAt"
//
// Missing or empty "accessToken" / "refreshToken" are treated as
// ErrSchemaIncompatible so callers can give a clear "not logged in" message.
func ParseCredentials(data []byte) (*Credentials, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty credentials file", ErrSchemaIncompatible)
	}
	if len(data) > maxFileSize {
		return nil, fmt.Errorf("schema: credentials file is %d bytes, exceeds max %d", len(data), maxFileSize)
	}

	// Minimal envelope — we don't unmarshal into a full struct to avoid
	// dropping unknown fields at the top level.
	var envelope struct {
		ClaudeAiOauth *struct {
			AccessToken  string `json:"accessToken"`
			RefreshToken string `json:"refreshToken"`
			ExpiresAt    string `json:"expiresAt"` // RFC3339
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("schema: credentials JSON parse error: %w", err)
	}
	if envelope.ClaudeAiOauth == nil {
		return nil, fmt.Errorf("%w: missing \"claudeAiOauth\" key", ErrSchemaIncompatible)
	}
	oauth := envelope.ClaudeAiOauth
	if oauth.AccessToken == "" {
		return nil, fmt.Errorf("%w: empty accessToken (not logged in or corrupted)", ErrSchemaIncompatible)
	}
	if oauth.RefreshToken == "" {
		return nil, fmt.Errorf("%w: empty refreshToken (not logged in or corrupted)", ErrSchemaIncompatible)
	}

	var expiresAt time.Time
	if oauth.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, oauth.ExpiresAt)
		if err != nil {
			// expiresAt is present but unparseable — treat as unknown expiry,
			// don't abort. Refresh logic treats zero time as "may be expired".
			expiresAt = time.Time{}
		} else {
			expiresAt = t
		}
	}

	return &Credentials{
		AccessToken:  oauth.AccessToken,
		RefreshToken: oauth.RefreshToken,
		ExpiresAt:    expiresAt,
		Raw:          data,
	}, nil
}
