// Package usage fetches per-account API usage from Anthropic's usage endpoint.
// A single call per profile is made — no caching, no background polling.
package usage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	clog "github.com/DhilipBinny/claudeorch/internal/log"
)

const (
	usageEndpoint  = "https://api.anthropic.com/api/oauth/usage"
	requestTimeout = 5 * time.Second
	oauthBetaHdr   = "oauth-2025-04-20"
)

// ErrUnauthorized is returned when the API returns 401 — token invalid or expired.
var ErrUnauthorized = errors.New("usage: unauthorized (token expired or invalid)")

// Usage holds the counters returned by the usage API.
type Usage struct {
	// UsedTokens is the total input+output tokens consumed this period.
	UsedTokens int64
	// LimitTokens is the plan limit for this period. Zero means no known limit.
	LimitTokens int64
	// ResetAt is when the usage counter resets. Zero means unknown.
	ResetAt time.Time
}

// PercentUsed returns the fraction of the limit consumed (0.0 – 1.0).
// Returns 0 when LimitTokens is 0 (unknown limit).
func (u *Usage) PercentUsed() float64 {
	if u.LimitTokens <= 0 {
		return 0
	}
	p := float64(u.UsedTokens) / float64(u.LimitTokens)
	if p > 1 {
		p = 1
	}
	return p
}

// Fetch retrieves usage for the account associated with accessToken.
// Returns ErrUnauthorized on 401. Other non-2xx statuses return a generic error.
// Times out after 5 seconds regardless of ctx deadline (whichever is shorter).
func Fetch(ctx context.Context, accessToken string) (*Usage, error) {
	tCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	slog.Debug("usage: fetching usage", "access_token", clog.Redact(accessToken))

	req, err := http.NewRequestWithContext(tCtx, http.MethodGet, usageEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("usage.Fetch: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("anthropic-beta", oauthBetaHdr)
	req.Header.Set("User-Agent", "claudeorch/dev")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("usage.Fetch: HTTP: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("usage.Fetch: read body: %w", err)
	}

	slog.Debug("usage: response", "status", resp.StatusCode)

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("usage.Fetch: status %d: %s", resp.StatusCode, body)
	}

	var raw struct {
		Data []struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"data"`
		Limit   int64  `json:"limit"`
		ResetAt string `json:"reset_at"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("usage.Fetch: parse: %w", err)
	}

	var total int64
	for _, d := range raw.Data {
		total += d.InputTokens + d.OutputTokens
	}

	var resetAt time.Time
	if raw.ResetAt != "" {
		resetAt, _ = time.Parse(time.RFC3339, raw.ResetAt)
	}

	return &Usage{
		UsedTokens:  total,
		LimitTokens: raw.Limit,
		ResetAt:     resetAt,
	}, nil
}
