package mux

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type WorkflowSession struct {
	Name      string `json:"name"`
	Profile   string `json:"profile"`
	Windows   int    `json:"windows"`
	CWD       string `json:"cwd"`
	ExtraArgs string `json:"extra_args,omitempty"`
}

type WorkflowDef struct {
	Name     string            `json:"name"`
	Sessions []WorkflowSession `json:"sessions"`
}

func workflowDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".claudeorch", "workflows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func workflowPath(name string) (string, error) {
	dir, err := workflowDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".json"), nil
}

func SaveWorkflow(name string, wf WorkflowDef) error {
	wf.Name = name
	path, err := workflowPath(name)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(wf, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func LoadWorkflow(name string) (*WorkflowDef, error) {
	path, err := workflowPath(name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("workflow %q not found", name)
		}
		return nil, err
	}
	var wf WorkflowDef
	if err := json.Unmarshal(data, &wf); err != nil {
		return nil, fmt.Errorf("parse workflow %q: %w", name, err)
	}
	return &wf, nil
}

func DeleteWorkflow(name string) error {
	path, err := workflowPath(name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("workflow %q not found", name)
		}
		return err
	}
	return nil
}

func ListWorkflows() ([]WorkflowDef, error) {
	dir, err := workflowDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var workflows []WorkflowDef
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".json")
		wf, err := LoadWorkflow(name)
		if err != nil {
			continue
		}
		workflows = append(workflows, *wf)
	}
	sort.Slice(workflows, func(i, j int) bool {
		return workflows[i].Name < workflows[j].Name
	})
	return workflows, nil
}

func SnapshotWorkflow(name string) (*WorkflowDef, error) {
	sessions, err := ListSessions()
	if err != nil {
		return nil, err
	}
	if len(sessions) == 0 {
		return nil, fmt.Errorf("no sessions to snapshot")
	}

	wf := &WorkflowDef{Name: name}
	for _, s := range sessions {
		cwd := ""
		if len(s.Windows) > 0 {
			cwd = s.Windows[0].CWD
		}
		extra := detectExtraArgs(SessionName(s.Name), 1)
		wf.Sessions = append(wf.Sessions, WorkflowSession{
			Name:      UserName(s.Name),
			Profile:   s.Profile,
			Windows:   len(s.Windows),
			CWD:       cwd,
			ExtraArgs: extra,
		})
	}
	return wf, nil
}

func detectExtraArgs(session string, windowIdx int) string {
	out, err := tmuxOutput("list-panes", "-t", fmt.Sprintf("%s:%d", session, windowIdx),
		"-F", "#{pane_start_command}")
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(strings.Split(out, "\n")[0])
	line = strings.Trim(line, "\"")
	// Pattern: "... launch --force <profile> [extra] && exec ..."
	idx := strings.Index(line, "launch --force ")
	if idx < 0 {
		return ""
	}
	after := line[idx+len("launch --force "):]
	// Skip the profile name (next space-delimited token, possibly quoted)
	after = strings.TrimSpace(after)
	if strings.HasPrefix(after, "'") {
		end := strings.IndexByte(after[1:], '\'')
		if end >= 0 {
			after = after[end+2:]
		}
	} else {
		sp := strings.IndexByte(after, ' ')
		if sp >= 0 {
			after = after[sp+1:]
		} else {
			return ""
		}
	}
	after = strings.TrimSpace(after)
	// Strip the trailing " && exec ..." part
	if ampIdx := strings.Index(after, "&&"); ampIdx >= 0 {
		after = strings.TrimSpace(after[:ampIdx])
	}
	return after
}
