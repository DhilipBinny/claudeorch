package migrate

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/DhilipBinny/claudeorch/internal/fsio"
)

// MemoryFile describes a memory file discovered in a project's memory/ dir.
type MemoryFile struct {
	Filename        string // e.g. "project_tk3_incident_24jul.md"
	Name            string // from frontmatter name field
	Description     string // from frontmatter description
	OriginSessionID string // from metadata.originSessionId
	CrossRefs       []string
}

var crossRefRe = regexp.MustCompile(`\[\[([^\]]+)\]\]`)

// DiscoverMemoryFiles scans a project's memory/ dir for .md files whose
// frontmatter originSessionId matches sessionID.
func DiscoverMemoryFiles(projectDir, sessionID string) ([]MemoryFile, error) {
	memDir := filepath.Join(projectDir, "memory")
	entries, err := os.ReadDir(memDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("migrate: read memory dir: %w", err)
	}

	var matched []MemoryFile
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".md" || e.Name() == "MEMORY.md" {
			continue
		}
		path := filepath.Join(memDir, e.Name())
		mf, err := parseMemoryFile(path)
		if err != nil || mf == nil {
			continue
		}
		if mf.OriginSessionID == sessionID {
			matched = append(matched, *mf)
		}
	}
	return matched, nil
}

func parseMemoryFile(path string) (*MemoryFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	content := string(data)

	mf := &MemoryFile{
		Filename: filepath.Base(path),
	}

	if !strings.HasPrefix(content, "---\n") {
		return nil, nil
	}
	endIdx := strings.Index(content[4:], "\n---")
	if endIdx < 0 {
		return nil, nil
	}
	frontmatter := content[4 : 4+endIdx]

	for _, line := range strings.Split(frontmatter, "\n") {
		trimmed := strings.TrimSpace(line)
		if k, v, ok := parseYAMLField(trimmed); ok {
			switch k {
			case "name":
				mf.Name = v
			case "description":
				mf.Description = v
			case "originSessionId":
				mf.OriginSessionID = v
			}
		}
	}

	body := content[4+endIdx+4:]
	for _, m := range crossRefRe.FindAllStringSubmatch(body, -1) {
		mf.CrossRefs = append(mf.CrossRefs, m[1])
	}

	if mf.OriginSessionID == "" {
		return nil, nil
	}
	return mf, nil
}

func parseYAMLField(line string) (key, value string, ok bool) {
	idx := strings.Index(line, ":")
	if idx < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:idx])
	value = strings.TrimSpace(line[idx+1:])
	value = strings.Trim(value, "\"'")
	return key, value, key != ""
}

// CopyMemoryFiles copies matched memory files from source to target memory/ dir.
// Creates the target memory/ dir if needed. Returns the list of files that had
// name conflicts and were skipped.
func CopyMemoryFiles(sourceProjectDir, targetProjectDir string, files []MemoryFile) (skipped []string, err error) {
	srcMemDir := filepath.Join(sourceProjectDir, "memory")
	dstMemDir := filepath.Join(targetProjectDir, "memory")
	if err := fsio.EnsureDir(dstMemDir, 0o700); err != nil {
		return nil, fmt.Errorf("migrate: create target memory dir: %w", err)
	}

	for _, mf := range files {
		srcPath := filepath.Join(srcMemDir, mf.Filename)
		dstPath := filepath.Join(dstMemDir, mf.Filename)

		if _, err := os.Stat(dstPath); err == nil {
			skipped = append(skipped, mf.Filename)
			continue
		}

		if err := copyFile(srcPath, dstPath); err != nil {
			return skipped, fmt.Errorf("migrate: copy memory file %s: %w", mf.Filename, err)
		}
	}
	return skipped, nil
}

// MoveMemoryFiles moves matched memory files from source to target memory/ dir.
func MoveMemoryFiles(sourceProjectDir, targetProjectDir string, files []MemoryFile) (skipped []string, err error) {
	srcMemDir := filepath.Join(sourceProjectDir, "memory")
	dstMemDir := filepath.Join(targetProjectDir, "memory")
	if err := fsio.EnsureDir(dstMemDir, 0o700); err != nil {
		return nil, fmt.Errorf("migrate: create target memory dir: %w", err)
	}

	for _, mf := range files {
		srcPath := filepath.Join(srcMemDir, mf.Filename)
		dstPath := filepath.Join(dstMemDir, mf.Filename)

		if _, err := os.Stat(dstPath); err == nil {
			skipped = append(skipped, mf.Filename)
			continue
		}

		if err := os.Rename(srcPath, dstPath); err != nil {
			if cpErr := copyFile(srcPath, dstPath); cpErr != nil {
				return skipped, fmt.Errorf("migrate: move memory file %s: %w", mf.Filename, cpErr)
			}
			if err := os.Remove(srcPath); err != nil {
				return skipped, fmt.Errorf("migrate: remove source memory file %s: %w", mf.Filename, err)
			}
		}
	}
	return skipped, nil
}

func copyFile(src, dst string) error {
	s, err := os.Open(src)
	if err != nil {
		return err
	}
	defer s.Close()

	d, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}

	if _, err := io.Copy(d, s); err != nil {
		d.Close()
		return err
	}
	if err := d.Sync(); err != nil {
		d.Close()
		return err
	}
	return d.Close()
}

// UpdateMemoryIndex adds entries for the given files to the target MEMORY.md
// and optionally removes them from the source MEMORY.md.
func UpdateMemoryIndex(sourceProjectDir, targetProjectDir string, files []MemoryFile, removeFromSource bool) error {
	if len(files) == 0 {
		return nil
	}

	targetIndex := filepath.Join(targetProjectDir, "memory", "MEMORY.md")
	if err := addToMemoryIndex(targetIndex, files); err != nil {
		return fmt.Errorf("migrate: update target MEMORY.md: %w", err)
	}

	if removeFromSource {
		sourceIndex := filepath.Join(sourceProjectDir, "memory", "MEMORY.md")
		if err := removeFromMemoryIndex(sourceIndex, files); err != nil {
			return fmt.Errorf("migrate: update source MEMORY.md: %w", err)
		}
	}
	return nil
}

func addToMemoryIndex(indexPath string, files []MemoryFile) error {
	existing := ""
	if data, err := os.ReadFile(indexPath); err == nil {
		existing = string(data)
	}

	existingFiles := make(map[string]bool)
	for _, line := range strings.Split(existing, "\n") {
		for _, mf := range files {
			if strings.Contains(line, mf.Filename) {
				existingFiles[mf.Filename] = true
			}
		}
	}

	var toAdd []string
	for _, mf := range files {
		if existingFiles[mf.Filename] {
			continue
		}
		title := mf.Name
		if title == "" {
			title = strings.TrimSuffix(mf.Filename, ".md")
		}
		desc := mf.Description
		if desc == "" {
			desc = "migrated memory"
		}
		toAdd = append(toAdd, fmt.Sprintf("- [%s](%s) — %s", title, mf.Filename, desc))
	}

	if len(toAdd) == 0 {
		return nil
	}

	content := strings.TrimRight(existing, "\n")
	if content == "" {
		content = "# Memory"
	}
	content += "\n" + strings.Join(toAdd, "\n") + "\n"

	if err := fsio.EnsureDir(filepath.Dir(indexPath), 0o700); err != nil {
		return err
	}
	return fsio.WriteFileAtomic(indexPath, []byte(content), 0o644)
}

func removeFromMemoryIndex(indexPath string, files []MemoryFile) error {
	data, err := os.ReadFile(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	removeSet := make(map[string]bool)
	for _, mf := range files {
		removeSet[mf.Filename] = true
	}

	var kept []string
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		remove := false
		for fn := range removeSet {
			if strings.Contains(line, fn) {
				remove = true
				break
			}
		}
		if !remove {
			kept = append(kept, line)
		}
	}

	result := strings.Join(kept, "\n")
	if !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	return fsio.WriteFileAtomic(indexPath, []byte(result), 0o644)
}

// BrokenCrossRefs returns cross-references in the given files that point to
// memory names NOT in the migrated set. These links will be broken after
// migration.
func BrokenCrossRefs(files []MemoryFile) []string {
	nameSet := make(map[string]bool)
	for _, mf := range files {
		if mf.Name != "" {
			nameSet[mf.Name] = true
		}
	}

	seen := make(map[string]bool)
	var broken []string
	for _, mf := range files {
		for _, ref := range mf.CrossRefs {
			if !nameSet[ref] && !seen[ref] {
				broken = append(broken, ref)
				seen[ref] = true
			}
		}
	}
	return broken
}
