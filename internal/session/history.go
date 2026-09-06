package session

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Conversation struct {
	SessionID  string    `json:"session_id"`
	CWD        string    `json:"cwd"`
	Profile    string    `json:"profile"`
	FirstPrompt string   `json:"first_prompt"`
	Messages   int       `json:"messages"`
	Size       int64     `json:"size"`
	ModTime    time.Time `json:"mod_time"`
	FilePath   string    `json:"file_path"`
}

func ScanConversations(claudeDir string, isolateDir string) ([]Conversation, error) {
	var convos []Conversation

	globalProjects := filepath.Join(claudeDir, "projects")
	if entries, err := scanProjectsDir(globalProjects, "(live)"); err == nil {
		convos = append(convos, entries...)
	}

	if isolateDir != "" {
		isoEntries, err := os.ReadDir(isolateDir)
		if err == nil {
			for _, e := range isoEntries {
				if !e.IsDir() {
					continue
				}
				profileProjects := filepath.Join(isolateDir, e.Name(), "projects")
				if entries, err := scanProjectsDir(profileProjects, e.Name()); err == nil {
					convos = append(convos, entries...)
				}
			}
		}
	}

	sort.Slice(convos, func(i, j int) bool {
		return convos[i].ModTime.After(convos[j].ModTime)
	})

	return convos, nil
}

func scanProjectsDir(projectsDir, profile string) ([]Conversation, error) {
	var convos []Conversation

	err := filepath.Walk(projectsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}

		rel, _ := filepath.Rel(projectsDir, path)
		dir := filepath.Dir(rel)
		cwd := decodeDirName(dir)

		sessionID := strings.TrimSuffix(filepath.Base(path), ".jsonl")

		convos = append(convos, Conversation{
			SessionID: sessionID,
			CWD:       cwd,
			Profile:   profile,
			Size:      info.Size(),
			ModTime:   info.ModTime(),
			FilePath:  path,
		})
		return nil
	})

	return convos, err
}

func decodeDirName(encoded string) string {
	if encoded == "." {
		return "?"
	}
	parts := strings.Split(encoded, string(filepath.Separator))
	if len(parts) > 0 {
		decoded := strings.ReplaceAll(parts[0], "-", "/")
		if !strings.HasPrefix(decoded, "/") {
			decoded = "/" + decoded
		}
		return decoded
	}
	return encoded
}

func ScanFirstPrompt(c *Conversation) {
	if c.FirstPrompt != "" {
		return
	}
	c.FirstPrompt, c.Messages = scanJSONLQuick(c.FilePath)
	if c.FirstPrompt == "" {
		c.FirstPrompt = "(empty)"
	}
}

func scanJSONLQuick(path string) (firstPrompt string, msgCount int) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0
	}
	defer f.Close()

	// Read only the first 256KB to find the first user prompt.
	buf := make([]byte, 256*1024)
	n, _ := f.Read(buf)
	if n == 0 {
		return "", 0
	}

	lines := strings.Split(string(buf[:n]), "\n")
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}

		if !strings.Contains(line, `"role"`) {
			continue
		}

		if !strings.Contains(line, `"user"`) {
			continue
		}

		msgCount++

		if firstPrompt == "" {
			var entry struct {
				Message *struct {
					Role    string      `json:"role"`
					Content interface{} `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal([]byte(line), &entry) == nil &&
				entry.Message != nil && entry.Message.Role == "user" {
				firstPrompt = extractText(entry.Message.Content)
			}
		}
	}

	return firstPrompt, msgCount
}

func extractText(content interface{}) string {
	switch v := content.(type) {
	case string:
		return truncateStr(v, 120)
	case []interface{}:
		for _, item := range v {
			if m, ok := item.(map[string]interface{}); ok {
				if m["type"] == "text" {
					if txt, ok := m["text"].(string); ok {
						return truncateStr(txt, 120)
					}
				}
			}
		}
	}
	return ""
}

func CloneConversation(c *Conversation) (*Conversation, error) {
	newID := newUUID()
	newPath := filepath.Join(filepath.Dir(c.FilePath), newID+".jsonl")

	src, err := os.Open(c.FilePath)
	if err != nil {
		return nil, fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	dst, err := os.Create(newPath)
	if err != nil {
		return nil, fmt.Errorf("create clone: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		os.Remove(newPath)
		return nil, fmt.Errorf("copy: %w", err)
	}

	info, _ := os.Stat(newPath)
	clone := &Conversation{
		SessionID:   newID,
		CWD:         c.CWD,
		Profile:     c.Profile,
		FirstPrompt: c.FirstPrompt,
		Messages:    c.Messages,
		Size:        info.Size(),
		ModTime:     info.ModTime(),
		FilePath:    newPath,
	}
	return clone, nil
}

func newUUID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func truncateStr(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	// Strip common non-user prefixes
	for _, prefix := range []string{"<fork-boilerplate>", "<local-command-caveat>", "<bash-input>"} {
		if strings.HasPrefix(s, prefix) {
			s = strings.TrimPrefix(s, prefix)
		}
	}
	s = strings.TrimSpace(s)
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
