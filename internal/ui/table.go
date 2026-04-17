package ui

import (
	"fmt"
	"io"
	"strings"

	"github.com/fatih/color"
)

// ProfileRow is one row in the list table.
type ProfileRow struct {
	Name        string
	Email       string
	OrgName     string
	Active      bool
	NeedsReauth bool
	UsagePct    float64 // -1 = unavailable
	UsageLabel  string  // e.g. "1.2M / 2.0M" or "-"
	ResetLabel  string  // e.g. "3d 14h" or "-"
}

// RenderTable writes the profile list table to w.
func RenderTable(w io.Writer, rows []ProfileRow) {
	if len(rows) == 0 {
		fmt.Fprintln(w, "No profiles. Run 'claudeorch add' to add one.")
		return
	}

	// Column widths (min values).
	const (
		colName  = 16
		colEmail = 28
		colOrg   = 16
		colBar   = 17 // bar + space
		colUsage = 18
		colReset = 8
	)

	// Header.
	bold := color.New(color.Bold).SprintFunc()
	header := fmt.Sprintf("%-*s  %-*s  %-*s  %-*s  %-*s  %s",
		colName, bold("PROFILE"),
		colEmail, bold("EMAIL"),
		colOrg, bold("ORG"),
		colBar, bold("USAGE"),
		colUsage, bold("TOKENS"),
		bold("RESETS"),
	)
	fmt.Fprintln(w, header)
	fmt.Fprintln(w, strings.Repeat("─", 100))

	for _, r := range rows {
		name := r.Name
		if r.Active {
			name = Colorize([]color.Attribute{color.FgCyan, color.Bold}, "* "+name)
		} else {
			name = "  " + name
		}
		if r.NeedsReauth {
			name += Colorize([]color.Attribute{color.FgRed}, " !")
		}

		bar := ""
		if r.UsagePct >= 0 {
			bar = Bar(r.UsagePct)
		} else {
			bar = "-"
		}

		fmt.Fprintf(w, "%-*s  %-*s  %-*s  %-*s  %-*s  %s\n",
			colName+extraColorBytes(name, r.Name), name,
			colEmail, truncate(r.Email, colEmail),
			colOrg, truncate(r.OrgName, colOrg),
			colBar+extraColorBytes(bar, plainBar(r.UsagePct)), bar,
			colUsage, truncate(r.UsageLabel, colUsage),
			r.ResetLabel,
		)
	}
}

// extraColorBytes returns the number of invisible ANSI bytes in colored, so
// fmt's width padding (which counts bytes) stays visually correct.
func extraColorBytes(colored, plain string) int {
	return len(colored) - len(plain)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

// plainBar returns the uncolored bar string for length calculation.
func plainBar(pct float64) string {
	if pct < 0 {
		return "-"
	}
	if pct > 1 {
		pct = 1
	}
	filled := int(pct * barWidth)
	empty := barWidth - filled
	return strings.Repeat(barFilledUni, filled) + strings.Repeat(barEmptyUni, empty)
}

// noColorEnabled returns true when color is globally disabled.
func noColorEnabled() bool {
	return color.NoColor
}
