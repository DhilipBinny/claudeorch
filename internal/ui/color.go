// Package ui provides terminal rendering helpers for claudeorch output:
// colored usage bars, profile tables, and ANSI/no-color detection.
package ui

import (
	"os"

	"github.com/fatih/color"
)

// Init must be called once at startup with the result of the global --no-color
// flag (or NoColor() from root.go). It permanently disables color if needed.
func Init(noColor bool) {
	if noColor || os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		color.NoColor = true
	}
}

// Color thresholds for usage bar coloring (percent, 0.0-1.0).
const (
	ThresholdWarn     = 0.60 // ≥60% → yellow
	ThresholdCritical = 0.85 // ≥85% → red
)

// BarColor returns the appropriate color function for a usage percentage.
func BarColor(pct float64) func(a ...interface{}) string {
	switch {
	case pct >= ThresholdCritical:
		return color.New(color.FgRed, color.Bold).SprintFunc()
	case pct >= ThresholdWarn:
		return color.New(color.FgYellow).SprintFunc()
	default:
		return color.New(color.FgGreen).SprintFunc()
	}
}

// Colorize wraps s in the given color attributes if color is enabled.
func Colorize(attrs []color.Attribute, s string) string {
	return color.New(attrs...).Sprint(s)
}
