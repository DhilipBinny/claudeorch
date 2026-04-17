package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectProfile_FromCLAUDECONFIGDIR(t *testing.T) {
	orch := t.TempDir()
	t.Setenv("CLAUDEORCH_HOME", orch)
	// Simulate an isolate dir — we just need the path to resolve.
	isolateDir := filepath.Join(orch, "isolate", "bala")
	if err := os.MkdirAll(isolateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", isolateDir)

	got := detectProfile()
	if got != "bala" {
		t.Errorf("detectProfile() = %q, want bala", got)
	}
}

func TestDetectProfile_NonIsolateConfigDir_ReturnsEmpty(t *testing.T) {
	orch := t.TempDir()
	t.Setenv("CLAUDEORCH_HOME", orch)
	t.Setenv("CLAUDE_CONFIG_DIR", "/not/an/isolate")
	// No store.json exists, so fallback also yields "".
	got := detectProfile()
	if got != "" {
		t.Errorf("detectProfile() = %q, want empty", got)
	}
}

func TestDetectProfile_FallsBackToActiveInStore(t *testing.T) {
	orch := t.TempDir()
	t.Setenv("CLAUDEORCH_HOME", orch)
	t.Setenv("CLAUDE_CONFIG_DIR", "") // explicitly unset
	storePath := filepath.Join(orch, "store.json")
	if err := os.WriteFile(storePath, []byte(`{
		"version": 1,
		"active": "dhilip",
		"profiles": {
			"dhilip": {
				"name": "dhilip",
				"email": "d@x.com",
				"org_uuid": "u",
				"org_name": "o",
				"created_at": "2026-01-01T00:00:00Z",
				"source": "oauth"
			}
		}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got := detectProfile()
	if got != "dhilip" {
		t.Errorf("detectProfile() = %q, want dhilip", got)
	}
}

func TestRenderStatusline_Happy_ContainsProfileAndDir(t *testing.T) {
	orch := t.TempDir()
	t.Setenv("CLAUDEORCH_HOME", orch)
	isolateDir := filepath.Join(orch, "isolate", "bala")
	_ = os.MkdirAll(isolateDir, 0o700)
	t.Setenv("CLAUDE_CONFIG_DIR", isolateDir)

	in := strings.NewReader(`{
		"workspace": {"current_dir": "/home/user/projects/demo"},
		"model": {"display_name": "Opus 4.7"},
		"context_window": {"used_percentage": 42.0}
	}`)
	var out strings.Builder
	if err := renderStatusline(in, &out); err != nil {
		t.Fatalf("renderStatusline: %v", err)
	}
	got := out.String()
	for _, want := range []string{"bala", "demo", "Opus 4.7", "42%"} {
		if !strings.Contains(got, want) {
			t.Errorf("statusline output missing %q:\n%s", want, got)
		}
	}
}

func TestRenderStatusline_InvalidJSON_DoesNotPanic(t *testing.T) {
	in := strings.NewReader(`not json`)
	var out strings.Builder
	if err := renderStatusline(in, &out); err != nil {
		t.Errorf("renderStatusline errored on bad input (should be graceful): %v", err)
	}
	if out.Len() == 0 {
		t.Error("renderStatusline produced no output on bad input")
	}
}

func TestRenderStatusline_EmptyInput_ProducesFallbackLine(t *testing.T) {
	in := strings.NewReader("")
	var out strings.Builder
	if err := renderStatusline(in, &out); err != nil {
		t.Fatal(err)
	}
	if out.Len() == 0 {
		t.Error("empty input should still produce some output")
	}
}

func TestRenderStatusline_MissingContextPct_OmitsCtxSegment(t *testing.T) {
	in := strings.NewReader(`{"workspace":{"current_dir":"/tmp/demo"},"model":{"display_name":"Opus"}}`)
	var out strings.Builder
	_ = renderStatusline(in, &out)
	if strings.Contains(out.String(), "ctx:") {
		t.Errorf("ctx segment should not appear when used_percentage is zero/missing:\n%s", out.String())
	}
}
