// Package oauth handles token refresh for saved profiles.
//
// Design: we preserve the full original credentials blob and merge only the
// changed fields (accessToken, refreshToken, expiresAt) back in. This means
// unknown fields added by future Claude Code versions are never silently
// discarded.
package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	tokenEndpoint  = "https://platform.claude.com/v1/oauth/token"
	clientID       = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	oauthBetaHdr   = "oauth-2025-04-20"
	requestTimeout = 5 * time.Second
)

// ErrInvalidGrant is returned when the server responds with error=invalid_grant.
// This means the refresh token is expired or revoked; the user must re-login.
var ErrInvalidGrant = errors.New("oauth: invalid_grant — refresh token expired, re-login required")

// ErrNetwork is returned for transient HTTP/network failures.
var ErrNetwork = errors.New("oauth: network error")

// ErrSchema is returned when the response is missing expected fields.
var ErrSchema = errors.New("oauth: response schema incompatible")

// Refresh exchanges the refresh token in credsBlob for a new access token and
// returns a new blob with only the changed fields merged in.
//
// credsBlob must be a valid .credentials.json blob. Unknown fields at any level
// are preserved verbatim in the returned blob.
//
// On ErrInvalidGrant the caller should set NeedsReauth=true on the profile.
func Refresh(ctx context.Context, credsBlob []byte) ([]byte, error) {
	// Extract refresh token from blob.
	var envelope struct {
		ClaudeAiOauth *struct {
			RefreshToken string `json:"refreshToken"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(credsBlob, &envelope); err != nil {
		return nil, fmt.Errorf("%w: parse credentials: %v", ErrSchema, err)
	}
	if envelope.ClaudeAiOauth == nil || envelope.ClaudeAiOauth.RefreshToken == "" {
		return nil, fmt.Errorf("%w: missing refreshToken", ErrSchema)
	}
	refreshToken := envelope.ClaudeAiOauth.RefreshToken

	// Call token endpoint.
	body := map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     clientID,
	}
	bodyJSON, _ := json.Marshal(body)

	tCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(tCtx, http.MethodPost, tokenEndpoint, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %v", ErrNetwork, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-beta", oauthBetaHdr)
	req.Header.Set("User-Agent", "claudeorch/dev")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNetwork, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: read response: %v", ErrNetwork, err)
	}

	// Check for invalid_grant before checking status code (some servers send 200 + error).
	var errResp struct {
		Error string `json:"error"`
	}
	if jsonErr := json.Unmarshal(respBody, &errResp); jsonErr == nil && errResp.Error == "invalid_grant" {
		return nil, ErrInvalidGrant
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("%w: status %d: %s", ErrNetwork, resp.StatusCode, respBody)
	}

	// Parse new token fields from response.
	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"` // seconds
	}
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		return nil, fmt.Errorf("%w: parse token response: %v", ErrSchema, err)
	}
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("%w: missing access_token in response", ErrSchema)
	}

	expiresAt := time.Now().UTC().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)

	// Merge new fields into the original blob, preserving all unknown fields.
	return mergeCredentials(credsBlob, tokenResp.AccessToken, tokenResp.RefreshToken, expiresAt)
}

// mergeCredentials merges the new OAuth fields into the original blob.
// It unmarshals into map[string]any, updates only the specific sub-fields,
// and re-marshals, so unknown fields are preserved.
func mergeCredentials(orig []byte, accessToken, refreshToken string, expiresAt time.Time) ([]byte, error) {
	var blob map[string]any
	if err := json.Unmarshal(orig, &blob); err != nil {
		return nil, fmt.Errorf("mergeCredentials: parse: %w", err)
	}

	oauth, ok := blob["claudeAiOauth"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("mergeCredentials: claudeAiOauth not a map")
	}

	oauth["accessToken"] = accessToken
	if refreshToken != "" {
		oauth["refreshToken"] = refreshToken
	}
	oauth["expiresAt"] = expiresAt.Format(time.RFC3339)
	blob["claudeAiOauth"] = oauth

	return json.Marshal(blob)
}
