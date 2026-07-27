package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeMemoryFile(t *testing.T, dir, filename, name, desc, sessionID, body string) {
	t.Helper()
	memDir := filepath.Join(dir, "memory")
	if err := os.MkdirAll(memDir, 0o700); err != nil {
		t.Fatal(err)
	}
	content := "---\n"
	content += "name: " + name + "\n"
	content += "description: " + desc + "\n"
	content += "metadata:\n"
	content += "  type: project\n"
	content += "  originSessionId: " + sessionID + "\n"
	content += "---\n"
	content += body
	if err := os.WriteFile(filepath.Join(memDir, filename), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeMemoryIndex(t *testing.T, dir, content string) {
	t.Helper()
	memDir := filepath.Join(dir, "memory")
	if err := os.MkdirAll(memDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memDir, "MEMORY.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverMemoryFiles(t *testing.T) {
	dir := t.TempDir()
	sid := "518a5bda-ad51-4f05-af3a-db131d6f853a"

	writeMemoryFile(t, dir, "incident_24jul.md", "incident-24jul", "incident notes", sid, "## Incident\nSome notes\n")
	writeMemoryFile(t, dir, "incident_25jul.md", "incident-25jul", "other notes", sid, "## Another\n")
	writeMemoryFile(t, dir, "unrelated.md", "unrelated", "unrelated", "other-session-id", "## Unrelated\n")

	files, err := DiscoverMemoryFiles(dir, sid)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2", len(files))
	}

	names := make(map[string]bool)
	for _, f := range files {
		names[f.Filename] = true
	}
	if !names["incident_24jul.md"] || !names["incident_25jul.md"] {
		t.Errorf("unexpected files: %v", names)
	}
}

func TestDiscoverMemoryFiles_NoMemoryDir(t *testing.T) {
	dir := t.TempDir()
	files, err := DiscoverMemoryFiles(dir, "any-id")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Errorf("got %d files, want 0", len(files))
	}
}

func TestDiscoverMemoryFiles_SkipsMEMORYMD(t *testing.T) {
	dir := t.TempDir()
	writeMemoryIndex(t, dir, "# Memory\n- [foo](foo.md)\n")
	writeMemoryFile(t, dir, "foo.md", "foo", "desc", "session-123", "body\n")

	files, err := DiscoverMemoryFiles(dir, "session-123")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1", len(files))
	}
	if files[0].Filename == "MEMORY.md" {
		t.Error("should not match MEMORY.md")
	}
}

func TestCopyMemoryFiles(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	sid := "test-session-id-1234"

	writeMemoryFile(t, srcDir, "note1.md", "note1", "desc", sid, "body1\n")
	writeMemoryFile(t, srcDir, "note2.md", "note2", "desc", sid, "body2\n")

	files, _ := DiscoverMemoryFiles(srcDir, sid)
	skipped, err := CopyMemoryFiles(srcDir, dstDir, files)
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 {
		t.Errorf("unexpected skipped: %v", skipped)
	}

	// Source files still exist.
	for _, f := range files {
		if _, err := os.Stat(filepath.Join(srcDir, "memory", f.Filename)); err != nil {
			t.Errorf("source file %s should still exist", f.Filename)
		}
	}
	// Target files exist.
	for _, f := range files {
		if _, err := os.Stat(filepath.Join(dstDir, "memory", f.Filename)); err != nil {
			t.Errorf("target file %s should exist: %v", f.Filename, err)
		}
	}
}

func TestMoveMemoryFiles(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	sid := "test-session-id-1234"

	writeMemoryFile(t, srcDir, "note1.md", "note1", "desc", sid, "body1\n")
	files, _ := DiscoverMemoryFiles(srcDir, sid)

	skipped, err := MoveMemoryFiles(srcDir, dstDir, files)
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 {
		t.Errorf("unexpected skipped: %v", skipped)
	}

	// Source file gone.
	if _, err := os.Stat(filepath.Join(srcDir, "memory", "note1.md")); !os.IsNotExist(err) {
		t.Error("source file should be removed after move")
	}
	// Target file exists.
	if _, err := os.Stat(filepath.Join(dstDir, "memory", "note1.md")); err != nil {
		t.Errorf("target file should exist: %v", err)
	}
}

func TestCopyMemoryFiles_SkipsExisting(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	sid := "test-session-id-1234"

	writeMemoryFile(t, srcDir, "note1.md", "note1", "desc", sid, "body1\n")
	writeMemoryFile(t, dstDir, "note1.md", "note1", "existing", sid, "existing content\n")

	files, _ := DiscoverMemoryFiles(srcDir, sid)
	skipped, err := CopyMemoryFiles(srcDir, dstDir, files)
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 1 || skipped[0] != "note1.md" {
		t.Errorf("expected note1.md skipped, got: %v", skipped)
	}

	// Target should still have original content.
	data, _ := os.ReadFile(filepath.Join(dstDir, "memory", "note1.md"))
	if !strings.Contains(string(data), "existing content") {
		t.Error("target file should not be overwritten")
	}
}

func TestUpdateMemoryIndex_AddToTarget(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	files := []MemoryFile{
		{Filename: "note1.md", Name: "note-1", Description: "first note"},
		{Filename: "note2.md", Name: "note-2", Description: "second note"},
	}

	if err := UpdateMemoryIndex(srcDir, dstDir, files, false); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dstDir, "memory", "MEMORY.md"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "note1.md") || !strings.Contains(content, "note2.md") {
		t.Errorf("target MEMORY.md should contain both entries: %s", content)
	}
	if !strings.Contains(content, "note-1") || !strings.Contains(content, "first note") {
		t.Errorf("target MEMORY.md should contain name and description: %s", content)
	}
}

func TestUpdateMemoryIndex_RemoveFromSource(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	writeMemoryIndex(t, srcDir, "# Memory\n- [note-1](note1.md) — first note\n- [keep](keep.md) — keep this\n")

	files := []MemoryFile{
		{Filename: "note1.md", Name: "note-1", Description: "first note"},
	}

	if err := UpdateMemoryIndex(srcDir, dstDir, files, true); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(srcDir, "memory", "MEMORY.md"))
	content := string(data)
	if strings.Contains(content, "note1.md") {
		t.Errorf("source MEMORY.md should not contain note1.md: %s", content)
	}
	if !strings.Contains(content, "keep.md") {
		t.Errorf("source MEMORY.md should still contain keep.md: %s", content)
	}
}

func TestUpdateMemoryIndex_NoDuplicates(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	writeMemoryIndex(t, dstDir, "# Memory\n- [note-1](note1.md) — already here\n")

	files := []MemoryFile{
		{Filename: "note1.md", Name: "note-1", Description: "first note"},
	}

	if err := UpdateMemoryIndex(srcDir, dstDir, files, false); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(dstDir, "memory", "MEMORY.md"))
	count := strings.Count(string(data), "note1.md")
	if count != 1 {
		t.Errorf("note1.md should appear exactly once, got %d: %s", count, string(data))
	}
}

func TestBrokenCrossRefs(t *testing.T) {
	files := []MemoryFile{
		{Name: "note-1", CrossRefs: []string{"note-2", "external-ref"}},
		{Name: "note-2", CrossRefs: []string{"note-1"}},
	}
	broken := BrokenCrossRefs(files)
	if len(broken) != 1 || broken[0] != "external-ref" {
		t.Errorf("broken = %v, want [external-ref]", broken)
	}
}

func TestBrokenCrossRefs_NoBroken(t *testing.T) {
	files := []MemoryFile{
		{Name: "note-1", CrossRefs: []string{"note-2"}},
		{Name: "note-2", CrossRefs: []string{"note-1"}},
	}
	broken := BrokenCrossRefs(files)
	if len(broken) != 0 {
		t.Errorf("broken = %v, want empty", broken)
	}
}

func TestParseMemoryFile_NoCrossRefs(t *testing.T) {
	dir := t.TempDir()
	sid := "session-abc"
	writeMemoryFile(t, dir, "simple.md", "simple", "desc", sid, "No cross refs here.\n")

	files, _ := DiscoverMemoryFiles(dir, sid)
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1", len(files))
	}
	if len(files[0].CrossRefs) != 0 {
		t.Errorf("crossrefs = %v, want empty", files[0].CrossRefs)
	}
}

func TestParseMemoryFile_WithCrossRefs(t *testing.T) {
	dir := t.TempDir()
	sid := "session-abc"
	writeMemoryFile(t, dir, "linked.md", "linked", "desc", sid, "See [[other-note]] and [[another]].\n")

	files, _ := DiscoverMemoryFiles(dir, sid)
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1", len(files))
	}
	if len(files[0].CrossRefs) != 2 {
		t.Errorf("crossrefs = %v, want 2 entries", files[0].CrossRefs)
	}
}

func TestParseMemoryFile_NoFrontmatter(t *testing.T) {
	dir := t.TempDir()
	memDir := filepath.Join(dir, "memory")
	os.MkdirAll(memDir, 0o700)
	os.WriteFile(filepath.Join(memDir, "plain.md"), []byte("# Just markdown\nNo frontmatter here.\n"), 0o600)

	files, err := DiscoverMemoryFiles(dir, "any")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Errorf("got %d files, want 0 (no frontmatter)", len(files))
	}
}

func TestParseMemoryFile_NoOriginSessionID(t *testing.T) {
	dir := t.TempDir()
	memDir := filepath.Join(dir, "memory")
	os.MkdirAll(memDir, 0o700)
	content := "---\nname: test\nmetadata:\n  type: project\n---\nBody\n"
	os.WriteFile(filepath.Join(memDir, "noorigin.md"), []byte(content), 0o600)

	files, err := DiscoverMemoryFiles(dir, "any")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Errorf("got %d files, want 0 (no originSessionId)", len(files))
	}
}
