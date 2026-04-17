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
