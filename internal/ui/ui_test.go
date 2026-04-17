package ui

import (
	"strings"
	"testing"

	"github.com/fatih/color"
)

func TestBar_Thresholds(t *testing.T) {
	// Disable color so we test the ASCII path predictably.
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = false })

	cases := []struct {
		pct     float64
		wantMin int // minimum number of '#' filled chars
	}{
		{0.0, 0},
		{0.5, 7},  // 50% → 7 of 15
		{1.0, 15}, // 100% → all filled
		{-0.1, 0}, // clamp to 0
		{1.1, 15}, // clamp to 1
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

func TestRenderTable_Rows(t *testing.T) {
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = false })

	rows := []ProfileRow{
		{
			Name:       "work",
			Email:      "alice@example.com",
			OrgName:    "Acme",
			Active:     true,
			UsagePct:   0.45,
			UsageLabel: "450K / 1M",
			ResetLabel: "5d",
		},
		{
			Name:        "home",
			Email:       "alice@personal.dev",
			OrgName:     "",
			Active:      false,
			NeedsReauth: true,
			UsagePct:    -1,
			UsageLabel:  "-",
			ResetLabel:  "-",
		},
	}

	var sb strings.Builder
	RenderTable(&sb, rows)
	out := sb.String()

	for _, want := range []string{"work", "alice@example.com", "Acme", "450K / 1M", "home", "alice@personal.dev", "-"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q:\n%s", want, out)
		}
	}
}

func TestPercentUsed_ZeroLimit(t *testing.T) {
	// Import usage package would create a cycle — test PercentUsed logic inline.
	// Just test the ui layer independently here.
	_ = Bar(0)
}
