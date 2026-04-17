package ui

import (
	"strings"
	"unicode/utf8"
)

const (
	barWidth       = 15
	barFilledUni   = "█"
	barEmptyUni    = "░"
	barFilledASCII = "#"
	barEmptyASCII  = "-"
)

// Bar renders a horizontal usage bar of barWidth characters.
// pct is a fraction in [0, 1]. Color is applied via BarColor if color is on.
//
// ASCII fallback is used when TERM=dumb or color is disabled, so the bar
// is always readable in plain-text environments.
func Bar(pct float64) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 1 {
		pct = 1
	}

	filled := int(pct * barWidth)
	empty := barWidth - filled

	filledChar := barFilledUni
	emptyChar := barEmptyUni
	if isASCIIMode() {
		filledChar = barFilledASCII
		emptyChar = barEmptyASCII
	}

	bar := strings.Repeat(filledChar, filled) + strings.Repeat(emptyChar, empty)

	// Count rune-width of bar for consistent display.
	_ = utf8.RuneCountInString(bar)

	colorFn := BarColor(pct)
	return colorFn(bar)
}

// isASCIIMode returns true when Unicode box-drawing chars should be avoided.
func isASCIIMode() bool {
	return noColorEnabled()
}
