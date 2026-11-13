package launch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// SavedAPIKeyMatches reports whether the isolate's .claude.json already holds
// apiKey as its saved Console key (primaryApiKey), i.e. Claude Code will
// authenticate with that exact key without ANTHROPIC_API_KEY being set.
// A missing or unreadable file, an empty key, or a different key all report
// false, so callers keep the explicit override.
func SavedAPIKeyMatches(isolateDir, apiKey string) bool {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(isolateDir, ".claude.json"))
	if err != nil {
		return false
	}
	var cfg struct {
		PrimaryAPIKey string `json:"primaryApiKey"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return false
	}
	return strings.TrimSpace(cfg.PrimaryAPIKey) == apiKey
}
