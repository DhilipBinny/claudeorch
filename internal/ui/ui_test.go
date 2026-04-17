package ui

import (
	"strings"
	"testing"

	"github.com/fatih/color"
)

func TestBar_Thresholds(t *testing.T) {
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = false })

	cases := []struct {
		pct     float64
		wantMin int
	}{
		{0.0, 0},
		{0.5, 7},
		{1.0, 15},
		{-0.1, 0},
		{1.1, 15},
	}

	for _, tc := range cases {
		bar := Bar(tc.pct)
		count := strings.Count(bar, barFilledASCII)
		if count < tc.wantMin {
			t.Errorf("Bar(%.2f) filled=%d, want>=%d: %q", tc.pct, count, tc.wantMin, bar)
		}
		if len(bar) == 0 {
			t.Errorf("Bar(%.2f) returned empty string", tc.pct)
		}
	}
}

func TestBar_Width(t *testing.T) {
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = false })

	for _, pct := range []float64{0, 0.3, 0.7, 1.0} {
		bar := Bar(pct)
		total := strings.Count(bar, barFilledASCII) + strings.Count(bar, barEmptyASCII)
		if total != barWidth {
			t.Errorf("Bar(%.2f) total chars = %d, want %d: %q", pct, total, barWidth, bar)
		}
	}
}

func TestRenderTable_Empty(t *testing.T) {
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = false })

	var sb strings.Builder
	RenderTable(&sb, nil)
	if !strings.Contains(sb.String(), "No profiles") {
		t.Errorf("empty table output: %q", sb.String())
	}
}

func TestRenderTable_Rows_ShowsBothWindows(t *testing.T) {
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = false })

	rows := []ProfileRow{
		{
			Name:          "work",
			Email:         "alice@example.com",
			OrgName:       "Acme",
			Active:        true,
			FiveHourPct:   0.09,
			SevenDayPct:   0.07,
			FiveHourReset: "2h15m",
			SevenDayReset: "6d5h",
		},
		{
			Name:          "home",
			Email:         "alice@personal.dev",
			OrgName:       "",
			NeedsReauth:   true,
			FiveHourPct:   -1,
			SevenDayPct:   -1,
			FiveHourReset: "-",
			SevenDayReset: "-",
		},
	}

	var sb strings.Builder
	RenderTable(&sb, rows)
	out := sb.String()

	for _, want := range []string{
		"5H", "7D",
		"PROFILE", "EMAIL", "ORG",
		"work", "home",
		"alice@example.com",
		"Acme",
		"2h15m", "6d5h",
		"9%", "7%",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q:\n%s", want, out)
		}
	}
}

func TestRenderTable_UnavailableUsage_ShowsDash(t *testing.T) {
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = false })

	rows := []ProfileRow{{
		Name:          "x",
		Email:         "x@x.com",
		FiveHourPct:   -1,
		SevenDayPct:   -1,
		FiveHourReset: "-",
		SevenDayReset: "-",
	}}
	var sb strings.Builder
	RenderTable(&sb, rows)
	out := sb.String()
	// No percentage should appear when usage is unavailable.
	if strings.Contains(out, "%") {
		t.Errorf("unavailable usage should not render %% sign:\n%s", out)
	}
}

// TestRenderTable_Alignment pins the fix from local testing: headers used to
// drift left of their column because bold() wraps them in ANSI escapes that
// the old byte-counting padding didn't compensate for, and the active-row
// "* " prefix shifted names right of the header. After the rewrite, every
// column's right edge lines up across header + separator + rows.
func TestRenderTable_Alignment(t *testing.T) {
	rows := []ProfileRow{
		{
			Name: "bala", Email: "balaprasannav2009@gmail.com", OrgName: "balaprasannav2009@gmail.com's Organization",
			Active: true, FiveHourPct: 0.17, SevenDayPct: 0.08,
			FiveHourReset: "1h11m", SevenDayReset: "6d5h",
		},
		{
			Name: "dhilip", Email: "dhilipkumar7235@gmail.com", OrgName: "dhilipkumar7235@gmail.com's Organization",
			FiveHourPct: 0.41, SevenDayPct: 0.03,
			FiveHourReset: "3h11m", SevenDayReset: "6d7h",
		},
	}
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = false })

	var sb strings.Builder
	RenderTable(&sb, rows)
	lines := strings.Split(strings.TrimRight(sb.String(), "\n"), "\n")
	if len(lines) < 4 {
		t.Fatalf("expected 4 lines (header, separator, 2 rows), got %d:\n%s", len(lines), sb.String())
	}
	header := visibleWidth(lines[0])
	sep := visibleWidth(lines[1])
	if header != sep {
		t.Errorf("header (%d chars) and separator (%d chars) don't match:\n%s\n%s",
			header, sep, lines[0], lines[1])
	}
	for i, row := range lines[2:] {
		vw := visibleWidth(strings.TrimRight(row, " "))
		// Rows may be SHORTER than header when their last column is empty
		// (status==""), but they must never be WIDER — that means a column
		// overflowed its computed width.
		if vw > header {
			t.Errorf("row %d width %d exceeds header width %d:\n  %s\n  %s",
				i, vw, header, lines[0], row)
		}
	}
}

func TestVisibleWidth_IgnoresANSI(t *testing.T) {
	cases := map[string]int{
		"":                                 0,
		"abc":                              3,
		"\x1b[1mabc\x1b[0m":                3,
		"\x1b[31;1mred bold\x1b[0m":        8,
		"plain\x1b[0m tail":                10,
		"█░░":                              3,
		"\x1b[32m██\x1b[0m░":               3,
	}
	for in, want := range cases {
		if got := visibleWidth(in); got != want {
			t.Errorf("visibleWidth(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestTruncate_Unicode(t *testing.T) {
	// Bar glyphs are multi-byte — truncate must count runes, not bytes.
	long := "███████████████" // 15 runes, 45 bytes
	got := truncate(long, 5)
	if visibleWidth(got) != 5 {
		t.Errorf("truncate kept %d visible chars (want 5) from %q", visibleWidth(got), long)
	}
}

func TestStripANSI(t *testing.T) {
	cases := map[string]string{
		"\x1b[31mred\x1b[0m":    "red",
		"plain":                 "plain",
		"\x1b[1;32mgreen\x1b[0m": "green",
	}
	for in, want := range cases {
		if got := stripANSI(in); got != want {
			t.Errorf("stripANSI(%q) = %q, want %q", in, got, want)
		}
	}
}
