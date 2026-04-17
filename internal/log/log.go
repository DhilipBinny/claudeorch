package log

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"gopkg.in/natefinch/lumberjack.v2"
)

// Options configures Setup.
type Options struct {
	// Debug, when true, raises the minimum level to DEBUG and switches stderr
	// output to structured JSON format. When false, stderr shows INFO+ in a
	// compact human-friendly format.
	Debug bool

	// LogFile is the absolute path of the log file (e.g., ~/.claudeorch/log/claudeorch.log).
	// Its parent directory is created with mode 0700 if missing.
	// The file is rotated by lumberjack: 5 backups × 2 MB each.
	//
	// Empty string disables file logging (stderr only). Useful for tests.
	LogFile string

	// Stderr is the writer for human-facing log output.
	// Defaults to os.Stderr when nil. Overrideable for tests.
	Stderr io.Writer
}

// Setup constructs a logger writing to stderr (always) and an on-disk file
// (when Options.LogFile is non-empty, rotated via lumberjack).
//
// Returns the logger and a cleanup function the caller must invoke on
// shutdown (usually via defer in main). Cleanup flushes and closes the
// lumberjack writer.
//
// The returned logger also becomes the process-wide default via
// slog.SetDefault, so package-level slog.Info/Debug/Error calls work
// correctly from any import site.
func Setup(opts Options) (*slog.Logger, func() error, error) {
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}

	level := slog.LevelInfo
	if opts.Debug {
		level = slog.LevelDebug
	}

	writers := []io.Writer{opts.Stderr}

	var closer func() error = func() error { return nil }

	if opts.LogFile != "" {
		dir := filepath.Dir(opts.LogFile)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, nil, fmt.Errorf("log: create log dir %s: %w", dir, err)
		}
		lj := &lumberjack.Logger{
			Filename:   opts.LogFile,
			MaxSize:    2, // megabytes
			MaxBackups: 5,
			MaxAge:     0, // no age-based rotation; size-only
			Compress:   false,
			LocalTime:  false,
		}
		writers = append(writers, lj)
		closer = lj.Close
	}

	out := io.MultiWriter(writers...)

	var handler slog.Handler
	if opts.Debug {
		handler = slog.NewJSONHandler(out, &slog.HandlerOptions{Level: level})
	} else {
		handler = slog.NewTextHandler(out, &slog.HandlerOptions{Level: level})
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)

	return logger, closer, nil
}
