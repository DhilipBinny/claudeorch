// Package migrate discovers and migrates Claude Code sessions between project
// directories. It reads and writes files inside ~/.claude/projects/ so that
// `/resume` in the new directory picks up migrated sessions.
package migrate

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/DhilipBinny/claudeorch/internal/fsio"
	"github.com/DhilipBinny/claudeorch/internal/paths"
)

// SessionInfo holds metadata about a discovered session, merged from the
// sessions-index.json and the JSONL file on disk.
type SessionInfo struct {
	SessionID    string
	Summary      string
	FirstPrompt  string
	Modified     string
	Created      string
	MessageCount int
	Source       string // "index", "filesystem", or "both"
	HasJSONL     bool
	JSONLSize    int64
	JSONLMtime   string
}

// IndexEntry is one entry inside sessions-index.json.
type IndexEntry struct {
	SessionID   string `json:"sessionId"`
	FullPath    string `json:"fullPath"`
	FileMtime   int64  `json:"fileMtime"`
	FirstPrompt string `json:"firstPrompt"`
	Summary     string `json:"summary"`
	MessageCount int   `json:"messageCount"`
	Created     string `json:"created"`
	Modified    string `json:"modified"`
	GitBranch   string `json:"gitBranch"`
	ProjectPath string `json:"projectPath"`
	IsSidechain bool   `json:"isSidechain"`
}

// SessionsIndex is the top-level structure of sessions-index.json.
type SessionsIndex struct {
	Version      int          `json:"version"`
	Entries      []IndexEntry `json:"entries"`
	OriginalPath string       `json:"originalPath"`
}

func isValidSessionID(sid string) bool {
	if sid == "" || len(sid) < 8 {
		return false
	}
	for _, c := range sid {
		if c == '/' || c == '\\' || c == '\x00' {
			return false
		}
	}
	return !strings.Contains(sid, "..")
}

func safePrefix(s string, n int) string {
	if len(s) < n {
		return s
	}
	return s[:n]
}

// PathToSlug converts an absolute directory path to Claude's project folder slug.
// e.g. /home/binny/Desktop → -home-binny-Desktop
func PathToSlug(dir string) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("migrate: empty directory path")
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("migrate: path must be absolute: %s", dir)
	}
	slug := strings.ReplaceAll(dir, string(filepath.Separator), "-")
	slug = strings.ReplaceAll(slug, "_", "-")
	if slug == "" || slug == "-" {
		return "", fmt.Errorf("migrate: invalid directory for migration: %s", dir)
	}
	return slug, nil
}

// ProjectDir returns the full path to a project's session directory
// inside Claude's config home.
func ProjectDir(dir string) (string, error) {
	slug, err := PathToSlug(dir)
	if err != nil {
		return "", err
	}
	projectsDir, err := paths.ClaudeProjectsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(projectsDir, slug), nil
}

// LoadSessionsIndex reads sessions-index.json from a project directory.
// Returns a default empty index if the file doesn't exist.
func LoadSessionsIndex(projectDir string) (*SessionsIndex, error) {
	indexPath := filepath.Join(projectDir, "sessions-index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &SessionsIndex{Version: 1, Entries: []IndexEntry{}}, nil
		}
		return nil, fmt.Errorf("migrate: read sessions-index: %w", err)
	}
	var idx SessionsIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("migrate: parse sessions-index: %w", err)
	}
	if idx.Entries == nil {
		idx.Entries = []IndexEntry{}
	}
	return &idx, nil
}

// SaveSessionsIndex writes sessions-index.json atomically.
func SaveSessionsIndex(projectDir string, idx *SessionsIndex) error {
	if err := fsio.EnsureDir(projectDir, 0o700); err != nil {
		return fmt.Errorf("migrate: ensure project dir: %w", err)
	}
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("migrate: marshal sessions-index: %w", err)
	}
	indexPath := filepath.Join(projectDir, "sessions-index.json")
	return fsio.WriteFileAtomic(indexPath, data, 0o644)
}

// DiscoverSessions finds all sessions in a project directory by merging
// the sessions-index.json with JSONL files on disk. Results are sorted
// by modified time descending (newest first).
func DiscoverSessions(projectDir string) ([]SessionInfo, error) {
	sessions := make(map[string]*SessionInfo)

	idx, err := LoadSessionsIndex(projectDir)
	if err != nil {
		return nil, err
	}
	for _, entry := range idx.Entries {
		sessions[entry.SessionID] = &SessionInfo{
			SessionID:    entry.SessionID,
			Summary:      entry.Summary,
			FirstPrompt:  entry.FirstPrompt,
			Modified:     entry.Modified,
			Created:      entry.Created,
			MessageCount: entry.MessageCount,
			Source:       "index",
		}
	}

	entries, err := os.ReadDir(projectDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("migrate: read project dir: %w", err)
	}

	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		sid := strings.TrimSuffix(e.Name(), ".jsonl")
		info, stErr := e.Info()
		if stErr != nil {
			continue
		}

		jsonlPath := filepath.Join(projectDir, e.Name())

		if existing, ok := sessions[sid]; ok {
			existing.HasJSONL = true
			existing.JSONLSize = info.Size()
			existing.JSONLMtime = info.ModTime().Format(time.RFC3339)
			if existing.Source == "index" {
				existing.Source = "both"
			}
			if existing.Summary == "" {
				existing.Summary = extractAITitle(jsonlPath)
			}
			continue
		}

		firstPrompt := extractFirstPrompt(jsonlPath)
		summary := extractAITitle(jsonlPath)
		sessions[sid] = &SessionInfo{
			SessionID:   sid,
			Summary:     summary,
			FirstPrompt: firstPrompt,
			Modified:    info.ModTime().Format(time.RFC3339),
			Created:     info.ModTime().Format(time.RFC3339),
			Source:      "filesystem",
			HasJSONL:    true,
			JSONLSize:   info.Size(),
			JSONLMtime:  info.ModTime().Format(time.RFC3339),
		}
	}

	result := make([]SessionInfo, 0, len(sessions))
	for _, s := range sessions {
		result = append(result, *s)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Modified > result[j].Modified
	})
	return result, nil
}

func extractAITitle(jsonlPath string) string {
	f, err := os.Open(jsonlPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil || fi.Size() == 0 {
		return ""
	}

	// ai-title entries are repeated throughout the file; read the last 256KB
	// to find the most recent one.
	const tailSize = 256 * 1024
	offset := fi.Size() - tailSize
	if offset < 0 {
		offset = 0
	}
	if offset > 0 {
		f.Seek(offset, 0)
	}

	buf, err := io.ReadAll(f)
	if err != nil {
		return ""
	}

	lines := strings.Split(string(buf), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if !strings.Contains(line, `"ai-title"`) {
			continue
		}
		var obj struct {
			Type    string `json:"type"`
			AITitle string `json:"aiTitle"`
		}
		if json.Unmarshal([]byte(line), &obj) == nil && obj.Type == "ai-title" && obj.AITitle != "" {
			return obj.AITitle
		}
	}
	return ""
}

// extractFirstPrompt reads a JSONL file and returns the first user message
// content, truncated to 120 characters.
func extractFirstPrompt(jsonlPath string) string {
	f, err := os.Open(jsonlPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	buf := make([]byte, 0, 4096)
	chunk := make([]byte, 4096)
	for {
		n, err := f.Read(chunk)
		buf = append(buf, chunk[:n]...)
		if prompt := scanForFirstPrompt(buf); prompt != "" {
			return prompt
		}
		if err != nil {
			break
		}
		if len(buf) > 512*1024 {
			break
		}
	}
	return ""
}

func scanForFirstPrompt(data []byte) string {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var obj map[string]any
		if json.Unmarshal([]byte(line), &obj) != nil {
			continue
		}
		if obj["type"] != "user" {
			continue
		}
		msg, ok := obj["message"].(map[string]any)
		if !ok {
			continue
		}
		content := msg["content"]
		switch c := content.(type) {
		case string:
			return truncate(c, 120)
		case []any:
			for _, block := range c {
				b, ok := block.(map[string]any)
				if !ok {
					continue
				}
				if b["type"] == "text" {
					if text, ok := b["text"].(string); ok {
						return truncate(text, 120)
					}
				}
			}
		}
	}
	return ""
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) > n {
		return string(runes[:n-3]) + "..."
	}
	return s
}
