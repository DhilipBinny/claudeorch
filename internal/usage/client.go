// Package usage fetches per-account API usage from Anthropic's usage endpoint.
// A single call per profile is made — no caching, no background polling.
//
// The endpoint returns utilization percentages (0-100) and reset timestamps
// for two rolling windows: the 5-hour burst window and the 7-day period.
// Additional per-model and per-feature breakdowns (seven_day_sonnet etc.)
// are returned by the server but not currently surfaced.
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
	defaultUsageEndpoint = "https://api.anthropic.com/api/oauth/usage"
	requestTimeout       = 5 * time.Second
	oauthBetaHdr         = "oauth-2025-04-20"
)

// usageEndpoint is a var (not const) so tests can point at an httptest.Server.
// Do NOT change it from production code.
var usageEndpoint = defaultUsageEndpoint

// ErrUnauthorized is returned when the API returns 401 — token invalid or expired.
var ErrUnauthorized = errors.New("usage: unauthorized (token expired or invalid)")

// Window is one reporting window (5-hour or 7-day).
type Window struct {
	// Percent is the fraction of the limit consumed, 0.0 – 1.0.
	// The server reports percentages 0-100; we divide by 100 at parse time.
	Percent float64
	// ResetsAt is when the window resets. Zero time means unknown.
	ResetsAt time.Time
}

// Usage holds the rolling-window counters returned by the usage API.
type Usage struct {
	FiveHour Window
	SevenDay Window
}

// rawWindow is the server's wire format for each window entry.
// resets_at is a pointer so we can distinguish "null" (explicitly absent)
// from an empty string, and RFC3339Nano handles both microsecond-precision
// timestamps with offsets and plain RFC3339.
type rawWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    *string `json:"resets_at"`
}

// Fetch retrieves usage for the account associated with accessToken.
// Returns ErrUnauthorized on 401. Other non-2xx statuses return a generic error.
// Times out after requestTimeout regardless of ctx deadline (whichever is shorter).
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
		FiveHour *rawWindow `json:"five_hour"`
		SevenDay *rawWindow `json:"seven_day"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("usage.Fetch: parse: %w", err)
	}

	return &Usage{
		FiveHour: toWindow(raw.FiveHour),
		SevenDay: toWindow(raw.SevenDay),
	}, nil
}

// toWindow converts the server's rawWindow into our Window type.
// Missing (nil) or malformed timestamps yield a zero time — the caller
// should format that as a dash.
func toWindow(raw *rawWindow) Window {
	if raw == nil {
		return Window{}
	}
	w := Window{Percent: raw.Utilization / 100.0}
	if w.Percent < 0 {
		w.Percent = 0
	}
	if w.Percent > 1 {
		w.Percent = 1
	}
	if raw.ResetsAt != nil && *raw.ResetsAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, *raw.ResetsAt); err == nil {
			w.ResetsAt = t
		}
	}
	return w
}
