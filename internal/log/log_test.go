package log

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSetup_StderrOnly covers the "no log file" path (e.g., tests, CI).
// Writes should appear on the provided writer; no files should be created.
func TestSetup_StderrOnly(t *testing.T) {
	var buf bytes.Buffer
	_, closer, err := Setup(Options{
		Debug:   false,
		LogFile: "",
		Stderr:  &buf,
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { _ = closer() })

	slog.Info("hello", "key", "value")

	out := buf.String()
	if !strings.Contains(out, "hello") {
		t.Errorf("stderr output missing message: %q", out)
	}
	if !strings.Contains(out, "key=value") {
		t.Errorf("stderr output missing attr: %q", out)
	}
}

// Debug=true → JSON handler. Verify output is JSON-shaped.
func TestSetup_DebugJSONFormat(t *testing.T) {
	var buf bytes.Buffer
	_, closer, err := Setup(Options{
		Debug:   true,
		LogFile: "",
		Stderr:  &buf,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closer() })

	slog.Info("hello", "key", "value")

	out := buf.String()
	// JSON handler produces a line starting with "{" and containing "\"msg\":\"hello\""
	if !strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Errorf("debug output not JSON-shaped: %q", out)
	}
	if !strings.Contains(out, `"msg":"hello"`) {
		t.Errorf("JSON missing msg field: %q", out)
	}
}

// Debug=false → INFO is logged, DEBUG is suppressed.
func TestSetup_InfoLevelSuppressesDebug(t *testing.T) {
	var buf bytes.Buffer
	_, closer, err := Setup(Options{
		Debug:   false,
		LogFile: "",
		Stderr:  &buf,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closer() })

	slog.Debug("should-not-appear")
	slog.Info("should-appear")

	out := buf.String()
	if strings.Contains(out, "should-not-appear") {
		t.Errorf("DEBUG leaked at INFO level: %q", out)
	}
	if !strings.Contains(out, "should-appear") {
		t.Errorf("INFO message missing: %q", out)
	}
}

// Debug=true → DEBUG level logs are captured.
func TestSetup_DebugLevelIncludesDebug(t *testing.T) {
	var buf bytes.Buffer
	_, closer, err := Setup(Options{
		Debug:   true,
		LogFile: "",
		Stderr:  &buf,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closer() })

	slog.Debug("debug-message")

	if !strings.Contains(buf.String(), "debug-message") {
		t.Errorf("DEBUG message missing from --debug output: %q", buf.String())
	}
}

// LogFile path triggers lumberjack rotation writer; log lines should land
// in BOTH stderr and the file.
func TestSetup_LogFile_WritesToBoth(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "log", "claudeorch.log")

	var buf bytes.Buffer
	_, closer, err := Setup(Options{
		Debug:   false,
		LogFile: logPath,
		Stderr:  &buf,
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	slog.Info("hello-file")

	// Close to flush lumberjack writer.
	if err := closer(); err != nil {
		t.Errorf("closer: %v", err)
	}

	// Stderr path:
	if !strings.Contains(buf.String(), "hello-file") {
		t.Errorf("stderr missing message: %q", buf.String())
	}

	// File path:
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if !strings.Contains(string(data), "hello-file") {
		t.Errorf("log file missing message: %q", string(data))
	}
}

// The log directory must be created with 0700 if missing.
func TestSetup_CreatesLogDir0700(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "nested", "log", "claudeorch.log")

	var buf bytes.Buffer
	_, closer, err := Setup(Options{LogFile: logPath, Stderr: &buf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closer() })

	info, err := os.Stat(filepath.Dir(logPath))
	if err != nil {
		t.Fatal(err)
	}
	mode := info.Mode().Perm()
	// On some systems umask may narrow further but never widen.
	// Require mode equals 0700 or is a subset.
	if mode&0o077 != 0 {
		t.Errorf("log dir mode = %04o, must have no group/other perms", mode)
	}
}
