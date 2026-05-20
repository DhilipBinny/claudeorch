package migrate

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DhilipBinny/claudeorch/internal/fsio"
	"github.com/DhilipBinny/claudeorch/internal/paths"
)

// ActionKind describes what a planned action does.
type ActionKind int

const (
	ActionMoveJSONL ActionKind = iota
	ActionMoveDir
	ActionNote
	ActionUpdateSourceIndex
	ActionUpdateTargetIndex
	ActionUpdateHistory
)

// Action is a planned step in a migration.
type Action struct {
	Kind    ActionKind
	Desc    string
	SrcPath string
	DstPath string
}

// MigrateOptions configures a migration.
type MigrateOptions struct {
	SourceDir string
	TargetDir string
	SessionID string // optional — empty means most recent
	DryRun    bool
}

// MigrateResult summarizes what happened.
type MigrateResult struct {
	SessionID  string
	BackupDir  string
	Actions    []string
	CwdRewrites int
}

// ResolveSession finds the target session for migration. If query is empty,
// uses the most recent. Resolution order:
//  1. Exact session ID match
//  2. Session ID prefix match
//  3. Case-insensitive substring match against summary and first prompt
//
// Returns an error if zero or multiple sessions match.
func ResolveSession(sessions []SessionInfo, query string) (*SessionInfo, error) {
	if len(sessions) == 0 {
		return nil, fmt.Errorf("migrate: no sessions found")
	}

	if query == "" {
		return &sessions[0], nil
	}

	// 1. Exact session ID match
	for i := range sessions {
		if sessions[i].SessionID == query {
			return &sessions[i], nil
		}
	}

	// 2. Session ID prefix match
	var prefixMatches []SessionInfo
	for _, s := range sessions {
		if strings.HasPrefix(s.SessionID, query) {
			prefixMatches = append(prefixMatches, s)
		}
	}
	if len(prefixMatches) == 1 {
		return &prefixMatches[0], nil
	}
	if len(prefixMatches) > 1 {
		return nil, fmtAmbiguousError(query, prefixMatches)
	}

	// 3. Case-insensitive substring match against summary + firstPrompt
	lower := strings.ToLower(query)
	var textMatches []SessionInfo
	for _, s := range sessions {
		haystack := strings.ToLower(s.Summary + " " + s.FirstPrompt)
		if strings.Contains(haystack, lower) {
			textMatches = append(textMatches, s)
		}
	}

	switch len(textMatches) {
	case 0:
		return nil, fmt.Errorf("migrate: no session matching %q (checked IDs, prefixes, and summary/prompt text)", query)
	case 1:
		return &textMatches[0], nil
	default:
		return nil, fmtAmbiguousError(query, textMatches)
	}
}

func fmtAmbiguousError(query string, matches []SessionInfo) error {
	var lines []string
	for _, m := range matches {
		label := m.Summary
		if label == "" {
			label = m.FirstPrompt
		}
		if len(label) > 60 {
			label = label[:57] + "..."
		}
		lines = append(lines, fmt.Sprintf("  %s  %s", m.SessionID, label))
	}
	return fmt.Errorf("migrate: %q matches %d sessions — be more specific:\n%s",
		query, len(matches), strings.Join(lines, "\n"))
}

// Plan builds the list of actions for a migration without executing them.
func Plan(opts MigrateOptions) ([]Action, *SessionInfo, error) {
	sourceProject, err := ProjectDir(opts.SourceDir)
	if err != nil {
		return nil, nil, err
	}

	if _, err := os.Stat(sourceProject); os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("migrate: no Claude project found for source: %s (looked for: %s)",
			opts.SourceDir, sourceProject)
	}

	sessions, err := DiscoverSessions(sourceProject)
	if err != nil {
		return nil, nil, err
	}

	session, err := ResolveSession(sessions, opts.SessionID)
	if err != nil {
		return nil, nil, err
	}

	targetProject, err := ProjectDir(opts.TargetDir)
	if err != nil {
		return nil, nil, err
	}

	sid := session.SessionID
	if !isValidSessionID(sid) {
		return nil, nil, fmt.Errorf("migrate: invalid session ID %q (must be ≥8 chars, no path separators or '..')", sid)
	}

	var actions []Action

	jsonlSrc := filepath.Join(sourceProject, sid+".jsonl")
	jsonlDst := filepath.Join(targetProject, sid+".jsonl")
	if _, err := os.Stat(jsonlSrc); err == nil {
		actions = append(actions, Action{
			Kind:    ActionMoveJSONL,
			Desc:    fmt.Sprintf("Move + rewrite cwd: %s → %s/", filepath.Base(jsonlSrc), filepath.Base(targetProject)),
			SrcPath: jsonlSrc,
			DstPath: jsonlDst,
		})
	}

	sessionDirSrc := filepath.Join(sourceProject, sid)
	sessionDirDst := filepath.Join(targetProject, sid)
	if info, err := os.Stat(sessionDirSrc); err == nil && info.IsDir() {
		actions = append(actions, Action{
			Kind:    ActionMoveDir,
			Desc:    fmt.Sprintf("Move session dir: %s/ → %s/", safePrefix(sid, 8), filepath.Base(targetProject)),
			SrcPath: sessionDirSrc,
			DstPath: sessionDirDst,
		})
	}

	configHome, err := paths.ClaudeConfigHome()
	if err != nil {
		return nil, nil, err
	}
	sessionEnvDir := filepath.Join(configHome, "session-env", sid)
	if _, err := os.Stat(sessionEnvDir); err == nil {
		actions = append(actions, Action{
			Kind: ActionNote,
			Desc: fmt.Sprintf("session-env/%s/ exists (session-global, no move needed)", safePrefix(sid, 8)),
		})
	}
	fileHistoryDir := filepath.Join(configHome, "file-history", sid)
	if _, err := os.Stat(fileHistoryDir); err == nil {
		actions = append(actions, Action{
			Kind: ActionNote,
			Desc: fmt.Sprintf("file-history/%s/ exists (session-global, no move needed)", safePrefix(sid, 8)),
		})
	}

	actions = append(actions, Action{
		Kind: ActionUpdateSourceIndex,
		Desc: fmt.Sprintf("Remove %s... from source sessions-index.json", safePrefix(sid, 12)),
	})
	actions = append(actions, Action{
		Kind: ActionUpdateTargetIndex,
		Desc: fmt.Sprintf("Add %s... to target sessions-index.json", safePrefix(sid, 12)),
	})
	actions = append(actions, Action{
		Kind: ActionUpdateHistory,
		Desc: "Rewrite project references in history.jsonl",
	})

	return actions, session, nil
}

// Execute runs the full migration: backup, move files, update indexes.
func Execute(opts MigrateOptions) (*MigrateResult, error) {
	if opts.SourceDir == opts.TargetDir {
		return nil, fmt.Errorf("migrate: source and target directories are the same: %s", opts.SourceDir)
	}

	actions, session, err := Plan(opts)
	if err != nil {
		return nil, err
	}
	sid := session.SessionID

	configHome, err := paths.ClaudeConfigHome()
	if err != nil {
		return nil, err
	}

	lockPath, err := paths.LockFile()
	if err != nil {
		return nil, fmt.Errorf("migrate: resolve lock file: %w", err)
	}
	release, err := fsio.AcquireLock(context.Background(), lockPath)
	if err != nil {
		return nil, fmt.Errorf("migrate: acquire lock: %w", err)
	}
	defer release()

	running, pid, err := IsSessionRunning(configHome, sid)
	if err != nil {
		return nil, err
	}
	if running {
		return nil, fmt.Errorf("migrate: session %s is currently running (PID %d) — close it first", sid, pid)
	}

	sourceProject, err := ProjectDir(opts.SourceDir)
	if err != nil {
		return nil, fmt.Errorf("migrate: resolve source project: %w", err)
	}
	targetProject, err := ProjectDir(opts.TargetDir)
	if err != nil {
		return nil, fmt.Errorf("migrate: resolve target project: %w", err)
	}

	backupDir, err := CreateBackupDir(sid)
	if err != nil {
		return nil, err
	}

	result := &MigrateResult{
		SessionID: sid,
		BackupDir: backupDir,
	}

	jsonlSrc := filepath.Join(sourceProject, sid+".jsonl")
	if err := BackupFile(backupDir, jsonlSrc, sid+".jsonl.bak"); err != nil {
		return nil, fmt.Errorf("migrate: backup JSONL failed — aborting: %w", err)
	}

	sourceIndexPath := filepath.Join(sourceProject, "sessions-index.json")
	if err := BackupFile(backupDir, sourceIndexPath, "source-sessions-index.json.bak"); err != nil {
		return nil, fmt.Errorf("migrate: backup source index failed — aborting: %w", err)
	}

	targetIndexPath := filepath.Join(targetProject, "sessions-index.json")
	if err := BackupFile(backupDir, targetIndexPath, "target-sessions-index.json.bak"); err != nil {
		return nil, fmt.Errorf("migrate: backup target index failed — aborting: %w", err)
	}

	historyFile, err := paths.ClaudeHistoryFile()
	if err != nil {
		return nil, fmt.Errorf("migrate: resolve history file: %w", err)
	}
	if err := BackupFile(backupDir, historyFile, "history.jsonl.bak"); err != nil {
		return nil, fmt.Errorf("migrate: backup history failed — aborting: %w", err)
	}

	if err := fsio.EnsureDir(targetProject, 0o700); err != nil {
		return nil, fmt.Errorf("migrate: create target project dir: %w", err)
	}

	for _, action := range actions {
		switch action.Kind {
		case ActionMoveJSONL:
			rewrites, err := moveJSONLWithRewrite(action.SrcPath, action.DstPath, opts.SourceDir, opts.TargetDir)
			if err != nil {
				return nil, fmt.Errorf("migrate: rewrite JSONL: %w", err)
			}
			result.CwdRewrites = rewrites
			result.Actions = append(result.Actions, fmt.Sprintf("[moved+rewritten] %s (%d cwd entries updated)", filepath.Base(action.SrcPath), rewrites))

		case ActionMoveDir:
			if info, err := os.Lstat(action.DstPath); err == nil {
				if info.Mode()&os.ModeSymlink != 0 {
					return nil, fmt.Errorf("migrate: target %s is a symlink — refusing to overwrite", action.DstPath)
				}
				if err := os.RemoveAll(action.DstPath); err != nil {
					return nil, fmt.Errorf("migrate: remove existing target dir: %w", err)
				}
			}
			if err := os.Rename(action.SrcPath, action.DstPath); err != nil {
				if err := copyDirAndRemove(action.SrcPath, action.DstPath); err != nil {
					return nil, fmt.Errorf("migrate: move session dir: %w", err)
				}
			}
			result.Actions = append(result.Actions, fmt.Sprintf("[moved] %s/", safePrefix(filepath.Base(action.SrcPath), 8)))

		case ActionNote:
			result.Actions = append(result.Actions, fmt.Sprintf("[info] %s", action.Desc))

		case ActionUpdateSourceIndex:
			if err := removeFromIndex(sourceProject, sid); err != nil {
				return nil, fmt.Errorf("migrate: update source index: %w", err)
			}
			result.Actions = append(result.Actions, fmt.Sprintf("[updated] Removed %s... from source index", safePrefix(sid, 12)))

		case ActionUpdateTargetIndex:
			if err := addToIndex(targetProject, opts.TargetDir, sid, session); err != nil {
				return nil, fmt.Errorf("migrate: update target index: %w", err)
			}
			result.Actions = append(result.Actions, fmt.Sprintf("[updated] Added %s... to target index", safePrefix(sid, 12)))

		case ActionUpdateHistory:
			updated, err := rewriteHistory(opts.SourceDir, opts.TargetDir, sid)
			if err != nil {
				return nil, fmt.Errorf("migrate: rewrite history: %w", err)
			}
			result.Actions = append(result.Actions, fmt.Sprintf("[updated] Rewrote %d history.jsonl entries", updated))
		}
	}

	return result, nil
}

func moveJSONLWithRewrite(srcPath, dstPath, oldCwd, newCwd string) (int, error) {
	src, err := os.Open(srcPath)
	if err != nil {
		return 0, err
	}
	defer src.Close()

	dstDir := filepath.Dir(dstPath)
	if err := fsio.EnsureDir(dstDir, 0o700); err != nil {
		return 0, err
	}

	tmp, err := os.CreateTemp(dstDir, "migrate-*.jsonl")
	if err != nil {
		return 0, err
	}
	tmpPath := tmp.Name()

	rewrites := 0
	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	writer := bufio.NewWriter(tmp)

	for scanner.Scan() {
		line := scanner.Text()
		var obj map[string]any
		if json.Unmarshal([]byte(line), &obj) == nil {
			if cwd, ok := obj["cwd"].(string); ok && cwd == oldCwd {
				obj["cwd"] = newCwd
				rewrites++
				if rewritten, err := json.Marshal(obj); err == nil {
					line = string(rewritten)
				}
			}
		}
		writer.WriteString(line)
		writer.WriteByte('\n')
	}

	if err := scanner.Err(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return 0, err
	}

	if err := writer.Flush(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return 0, err
	}

	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return 0, err
	}
	tmp.Close()

	if err := os.Rename(tmpPath, dstPath); err != nil {
		os.Remove(tmpPath)
		return 0, err
	}

	if err := os.Remove(srcPath); err != nil {
		return rewrites, fmt.Errorf("migrate: remove source JSONL after move: %w", err)
	}
	return rewrites, nil
}

func removeFromIndex(projectDir, sessionID string) error {
	idx, err := LoadSessionsIndex(projectDir)
	if err != nil {
		return err
	}
	filtered := make([]IndexEntry, 0, len(idx.Entries))
	for _, e := range idx.Entries {
		if e.SessionID != sessionID {
			filtered = append(filtered, e)
		}
	}
	idx.Entries = filtered
	return SaveSessionsIndex(projectDir, idx)
}

func addToIndex(projectDir, targetDir, sessionID string, session *SessionInfo) error {
	idx, err := LoadSessionsIndex(projectDir)
	if err != nil {
		return err
	}
	idx.OriginalPath = targetDir

	filtered := make([]IndexEntry, 0, len(idx.Entries))
	for _, e := range idx.Entries {
		if e.SessionID != sessionID {
			filtered = append(filtered, e)
		}
	}

	now := time.Now()
	created := session.Created
	if created == "" {
		created = now.Format(time.RFC3339)
	}
	modified := session.Modified
	if modified == "" {
		modified = now.Format(time.RFC3339)
	}

	filtered = append(filtered, IndexEntry{
		SessionID:    sessionID,
		FullPath:     filepath.Join(projectDir, sessionID+".jsonl"),
		FileMtime:    now.UnixMilli(),
		FirstPrompt:  session.FirstPrompt,
		Summary:      session.Summary,
		MessageCount: session.MessageCount,
		Created:      created,
		Modified:     modified,
		ProjectPath:  targetDir,
	})

	idx.Entries = filtered
	return SaveSessionsIndex(projectDir, idx)
}

func rewriteHistory(oldDir, newDir, sessionID string) (int, error) {
	historyFile, err := paths.ClaudeHistoryFile()
	if err != nil {
		return 0, err
	}

	data, err := os.ReadFile(historyFile)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	var lines []string
	updated := 0

	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			lines = append(lines, line)
			continue
		}
		var obj map[string]any
		if json.Unmarshal([]byte(line), &obj) != nil {
			lines = append(lines, line)
			continue
		}
		if obj["sessionId"] == sessionID && obj["project"] == oldDir {
			obj["project"] = newDir
			if rewritten, err := json.Marshal(obj); err == nil {
				lines = append(lines, string(rewritten))
				updated++
				continue
			}
		}
		lines = append(lines, line)
	}

	result := strings.Join(lines, "\n")
	if err := fsio.WriteFileAtomic(historyFile, []byte(result), 0o644); err != nil {
		return 0, err
	}
	return updated, nil
}

func copyDirAndRemove(src, dst string) error {
	if err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("migrate: refusing to copy symlink: %s", path)
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}

		srcFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		dstFile, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
		if err != nil {
			return err
		}
		defer dstFile.Close()

		if _, err := io.Copy(dstFile, srcFile); err != nil {
			return err
		}
		return dstFile.Sync()
	}); err != nil {
		return err
	}
	return os.RemoveAll(src)
}
