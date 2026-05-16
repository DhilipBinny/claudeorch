package usage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/DhilipBinny/claudeorch/internal/fsio"
	"github.com/DhilipBinny/claudeorch/internal/paths"
)

const cacheTTL = 60 * time.Second

type cacheEntry struct {
	FiveHourPct   float64   `json:"five_hour_pct"`
	FiveHourReset time.Time `json:"five_hour_reset,omitempty"`
	SevenDayPct   float64   `json:"seven_day_pct"`
	SevenDayReset time.Time `json:"seven_day_reset,omitempty"`
	FetchedAt     time.Time `json:"fetched_at"`
}

func cachePath(profileName string) (string, error) {
	dir, err := paths.CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, profileName+"-usage.json"), nil
}

// LoadCached returns a cached Usage if it exists and is younger than cacheTTL.
// Returns nil (no error) on miss, stale, or any read failure.
func LoadCached(profileName string) *Usage {
	p, err := cachePath(profileName)
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var e cacheEntry
	if json.Unmarshal(data, &e) != nil {
		return nil
	}
	if time.Since(e.FetchedAt) > cacheTTL {
		return nil
	}
	return &Usage{
		FiveHour: Window{Percent: e.FiveHourPct, ResetsAt: e.FiveHourReset},
		SevenDay: Window{Percent: e.SevenDayPct, ResetsAt: e.SevenDayReset},
	}
}

// LoadStale returns cached data regardless of age.
// Used as a fallback when the API is unavailable (429, network error, etc.)
// so the user sees slightly-old data instead of dashes.
func LoadStale(profileName string) *Usage {
	p, err := cachePath(profileName)
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var e cacheEntry
	if json.Unmarshal(data, &e) != nil {
		return nil
	}
	return &Usage{
		FiveHour: Window{Percent: e.FiveHourPct, ResetsAt: e.FiveHourReset},
		SevenDay: Window{Percent: e.SevenDayPct, ResetsAt: e.SevenDayReset},
	}
}

// SaveCache writes a usage response to disk for profileName.
// Errors are returned but callers may safely ignore them — a failed cache
// write just means the next call hits the API.
func SaveCache(profileName string, u *Usage) error {
	p, err := cachePath(profileName)
	if err != nil {
		return err
	}
	if err := fsio.EnsureDir(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("usage cache: mkdir: %w", err)
	}
	e := cacheEntry{
		FiveHourPct:   u.FiveHour.Percent,
		FiveHourReset: u.FiveHour.ResetsAt,
		SevenDayPct:   u.SevenDay.Percent,
		SevenDayReset: u.SevenDay.ResetsAt,
		FetchedAt:     time.Now(),
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return fsio.WriteFileAtomic(p, data, 0o644)
}
