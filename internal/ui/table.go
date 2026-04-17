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

// columnGutter is the number of spaces between columns in the rendered table.
const columnGutter = 2

// RenderTable writes the profile list table to w.
//
// Alignment works by computing each column's width from the VISIBLE (ANSI-
// stripped) length of every cell — header and rows alike — then padding
// every cell to that width before emitting. The active-profile marker ("* ")
// and any color escapes are part of the cell content but don't affect the
// width calculation.
func RenderTable(w io.Writer, rows []ProfileRow) {
	if len(rows) == 0 {
		fmt.Fprintln(w, "No profiles. Run 'claudeorch add' to add one.")
		return
	}

	bold := color.New(color.Bold).SprintFunc()
	cyanBold := color.New(color.FgCyan, color.Bold).SprintFunc()
	red := color.New(color.FgRed, color.Bold).SprintFunc()

	headers := []string{
		bold("PROFILE"),
		bold("EMAIL"),
		bold("ORG"),
		bold("5H"),
		bold("5H-RESET"),
		bold("7D"),
		bold("7D-RESET"),
		bold("STATUS"),
	}

	const (
		maxEmail = 30
		maxOrg   = 20
	)

	cells := [][]string{headers}

	for _, r := range rows {
		namePrefix := "  "
		name := r.Name
		if r.Active {
			namePrefix = "* "
			name = cyanBold(r.Name)
		}
		cells = append(cells, []string{
			namePrefix + name,
			truncate(r.Email, maxEmail),
			truncate(r.OrgName, maxOrg),
			renderBarCol(r.FiveHourPct),
			r.FiveHourReset,
			renderBarCol(r.SevenDayPct),
			r.SevenDayReset,
			statusLabel(r, red),
		})
	}

	// Compute the visible-width of each column across header + all rows.
	numCols := len(headers)
	widths := make([]int, numCols)
	for _, row := range cells {
		for i, cell := range row {
			if vw := visibleWidth(cell); vw > widths[i] {
				widths[i] = vw
			}
		}
	}

	gutter := strings.Repeat(" ", columnGutter)
	writeRow := func(row []string) {
		for i, cell := range row {
			fmt.Fprint(w, cell)
			pad := widths[i] - visibleWidth(cell)
			if pad > 0 {
				fmt.Fprint(w, strings.Repeat(" ", pad))
			}
			if i < numCols-1 {
				fmt.Fprint(w, gutter)
			}
		}
		fmt.Fprintln(w)
	}

	// Header + separator + rows.
	writeRow(cells[0])
	totalWidth := 0
	for _, wdt := range widths {
		totalWidth += wdt + columnGutter
	}
	totalWidth -= columnGutter
	fmt.Fprintln(w, strings.Repeat("─", totalWidth))
	for _, row := range cells[1:] {
		writeRow(row)
	}
}

// renderBarCol returns "<bar> <pct>%" or "  —  " for the bar column.
// A dash placeholder has fixed width matching the 4-char "100%" suffix
// so columns stay aligned when usage is unavailable.
func renderBarCol(pct float64) string {
	if pct < 0 {
		return "—"
	}
	return fmt.Sprintf("%s %3d%%", Bar(pct), int(pct*100+0.5))
}

// statusLabel returns the rightmost-column label for a row.
func statusLabel(r ProfileRow, red func(a ...interface{}) string) string {
	if r.NeedsReauth {
		return red("!reauth")
	}
	if r.Active {
		return "active"
	}
	return ""
}

// visibleWidth returns the printed (non-ANSI) character count of s.
// Counts runes for correctness with Unicode bar glyphs like █/░/…, but skips
// CSI escape sequences ("\x1b[" ... letter) so colorized cells align with
// plain ones.
func visibleWidth(s string) int {
	count := 0
	i := 0
	for i < len(s) {
		r, size := decodeRune(s[i:])
		if r == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			// Skip CSI sequence up to and including the terminator letter.
			j := i + 2
			for j < len(s) {
				c := s[j]
				if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
					j++
					break
				}
				j++
			}
			i = j
			continue
		}
		count++
		i += size
	}
	return count
}

// decodeRune is a tiny UTF-8 decoder that doesn't pull in unicode/utf8
// in this hot path. Returns (rune, byte-size). Invalid bytes count as 1.
func decodeRune(s string) (rune, int) {
	if len(s) == 0 {
		return 0, 0
	}
	b := s[0]
	switch {
	case b < 0x80:
		return rune(b), 1
	case b < 0xC0:
		// Continuation byte without leader — malformed; treat as 1 byte.
		return rune(b), 1
	case b < 0xE0:
		if len(s) < 2 {
			return rune(b), 1
		}
		return rune(b&0x1F)<<6 | rune(s[1]&0x3F), 2
	case b < 0xF0:
		if len(s) < 3 {
			return rune(b), 1
		}
		return rune(b&0x0F)<<12 | rune(s[1]&0x3F)<<6 | rune(s[2]&0x3F), 3
	default:
		if len(s) < 4 {
			return rune(b), 1
		}
		return rune(b&0x07)<<18 | rune(s[1]&0x3F)<<12 | rune(s[2]&0x3F)<<6 | rune(s[3]&0x3F), 4
	}
}

// stripANSI removes ANSI color escapes (exported for tests).
func stripANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) {
				c := s[j]
				if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
					j++
					break
				}
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func truncate(s string, max int) string {
	if s == "" {
		return ""
	}
	if visibleWidth(s) <= max {
		return s
	}
	// Truncate by rune count, not byte count, so mid-multibyte doesn't corrupt.
	out := make([]byte, 0, len(s))
	w := 0
	i := 0
	for i < len(s) && w < max-1 {
		_, size := decodeRune(s[i:])
		out = append(out, s[i:i+size]...)
		i += size
		w++
	}
	return string(out) + "…"
}

// noColorEnabled returns true when color is globally disabled.
func noColorEnabled() bool {
	return color.NoColor
}
