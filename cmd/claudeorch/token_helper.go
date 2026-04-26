package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/DhilipBinny/claudeorch/internal/creds"
	"github.com/DhilipBinny/claudeorch/internal/fsio"
	"github.com/DhilipBinny/claudeorch/internal/oauth"
	"github.com/DhilipBinny/claudeorch/internal/paths"
	"github.com/DhilipBinny/claudeorch/internal/profile"
	"github.com/DhilipBinny/claudeorch/internal/schema"
)

// freshAccessToken returns a valid access token for the named profile,
// transparently refreshing if the stored one has expired. This is standard
// OAuth 2.0 client behaviour — the caller never sees an expired token.
//
// Flow:
//  1. Read the profile's credentials.json
//  2. If access token hasn't expired → return it immediately (refreshed=false)
//  3. If expired → call Anthropic's token endpoint with the refresh token
//  4. On success: save new tokens to profile (+ live/isolate if owned),
//     return the fresh access token (refreshed=true)
//  5. On invalid_grant: mark needs_reauth on the profile, return error
//  6. On network error: return error (caller shows "-")
//
// Returns (accessToken, refreshed, error):
//   - refreshed=true when an OAuth refresh was performed (caller should save store)
//   - refreshed=false when the existing token was still valid (no save needed)
//
// The store is modified in-place when needs_reauth is set or tokens are
// refreshed. Callers should save the store when refreshed=true.
func freshAccessToken(name string, store *profile.Store, storePath string) (string, bool, error) {
	profileDir, err := paths.ProfileDir(name)
	if err != nil {
		return "", false, err
	}
	credsPath := filepath.Join(profileDir, "credentials.json")
	credsData, err := os.ReadFile(credsPath)
	if err != nil {
		return "", false, fmt.Errorf("read credentials for %q: %w", name, err)
	}
	parsedCreds, err := schema.ParseCredentials(credsData)
	if err != nil {
		return "", false, fmt.Errorf("parse credentials for %q: %w", name, err)
	}

	// If the access token is still valid, use it directly — no refresh needed.
	if !parsedCreds.ExpiresAt.IsZero() && time.Now().Before(parsedCreds.ExpiresAt) {
		return parsedCreds.AccessToken, false, nil
	}

	// Access token expired — try auto-refresh.
	slog.Debug("token: access token expired, auto-refreshing", "profile", name)
	newData, err := oauth.Refresh(context.Background(), credsData)
	if err != nil {
		if isInvalidGrant(err) {
			store.Profiles[name].NeedsReauth = true
			slog.Debug("token: refresh returned invalid_grant", "profile", name)
		}
		return "", false, fmt.Errorf("auto-refresh %q: %w", name, err)
	}

	// Save refreshed credentials to profile.
	if writeErr := fsio.WriteFileAtomic(credsPath, newData, 0o600); writeErr != nil {
		slog.Warn("token: failed to save refreshed credentials", "profile", name, "err", writeErr)
	}

	// Sync to live if this is the active profile.
	if store.IsActive(name) {
		if livePath, lErr := paths.ClaudeCredentialsPath(); lErr == nil {
			_ = creds.WriteLive(livePath, newData)
		}
	}

	// Sync to isolate if it exists.
	if isolateDir, iErr := paths.IsolateDir(name); iErr == nil {
		isolateCreds := filepath.Join(isolateDir, ".credentials.json")
		if _, statErr := os.Stat(isolateCreds); statErr == nil {
			_ = fsio.WriteFileAtomic(isolateCreds, newData, 0o600)
		}
	}

	// Update store metadata.
	newParsed, parseErr := schema.ParseCredentials(newData)
	if parseErr == nil {
		store.Profiles[name].TokensLastSeenAt = newParsed.ExpiresAt
		store.Profiles[name].NeedsReauth = false
		return newParsed.AccessToken, true, nil
	}
	return "", true, fmt.Errorf("parse refreshed credentials for %q: %w", name, parseErr)
}

func isInvalidGrant(err error) bool {
	return errors.Is(err, oauth.ErrInvalidGrant)
}
