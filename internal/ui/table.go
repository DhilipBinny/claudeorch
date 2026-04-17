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

	// FiveHourPct / SevenDayPct are 0.0–1.0, or -1 when usage is unavailable.
	FiveHourPct float64
	SevenDayPct float64

	// FiveHourReset / SevenDayReset are short human labels like "2h15m" or "-".
	FiveHourReset string
	SevenDayReset string
}

// RenderTable writes the profile list table to w.
func RenderTable(w io.Writer, rows []ProfileRow) {
	if len(rows) == 0 {
		fmt.Fprintln(w, "No profiles. Run 'claudeorch add' to add one.")
		return
	}

	const (
		colName  = 14
		colEmail = 26
		colOrg   = 14
		colBar   = 22 // bar (15) + " " + pct% (e.g. " 100%")
		colReset = 8
	)

	bold := color.New(color.Bold).SprintFunc()
	header := fmt.Sprintf("%-*s  %-*s  %-*s  %-*s  %-*s  %-*s  %-*s  %s",
		colName, bold("PROFILE"),
		colEmail, bold("EMAIL"),
		colOrg, bold("ORG"),
		colBar, bold("5H"),
		colReset, bold("5H RESET"),
		colBar, bold("7D"),
		colReset, bold("7D RESET"),
		bold("STATUS"),
	)
	fmt.Fprintln(w, header)
	fmt.Fprintln(w, strings.Repeat("─", 140))

	for _, r := range rows {
		name := r.Name
		if r.Active {
			name = Colorize([]color.Attribute{color.FgCyan, color.Bold}, "* "+name)
		} else {
			name = "  " + name
		}

		status := ""
		if r.NeedsReauth {
			status = Colorize([]color.Attribute{color.FgRed, color.Bold}, "!reauth")
		} else if r.Active {
			status = "active"
		}

		fiveBar := renderBarCol(r.FiveHourPct)
		sevenBar := renderBarCol(r.SevenDayPct)

		fmt.Fprintf(w, "%-*s  %-*s  %-*s  %-*s  %-*s  %-*s  %-*s  %s\n",
			colName+extraColorBytes(name, stripANSI(name)), name,
			colEmail, truncate(r.Email, colEmail),
			colOrg, truncate(r.OrgName, colOrg),
			colBar+extraColorBytes(fiveBar, stripANSI(fiveBar)), fiveBar,
			colReset, truncate(r.FiveHourReset, colReset),
			colBar+extraColorBytes(sevenBar, stripANSI(sevenBar)), sevenBar,
			colReset, truncate(r.SevenDayReset, colReset),
			status,
		)
	}
}

// renderBarCol returns "<bar> <pct>%" or "unavailable" for the bar column.
func renderBarCol(pct float64) string {
	if pct < 0 {
		return "      -     "
	}
	return fmt.Sprintf("%s %3d%%", Bar(pct), int(pct*100+0.5))
}

// stripANSI removes ANSI color escapes so width math works on the visible text.
func stripANSI(s string) string {
	for {
		i := strings.Index(s, "\x1b[")
		if i < 0 {
			return s
		}
		j := strings.IndexByte(s[i:], 'm')
		if j < 0 {
			return s
		}
		s = s[:i] + s[i+j+1:]
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

// noColorEnabled returns true when color is globally disabled.
func noColorEnabled() bool {
	return color.NoColor
}
