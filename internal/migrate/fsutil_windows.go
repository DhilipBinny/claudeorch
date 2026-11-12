//go:build windows

package migrate

import "os"

// openNoFollow has no os.OpenFile equivalent on Windows.
const openNoFollow = 0

// hasOtherLinks is conservative on Windows, where link counts are not
// exposed through os.FileInfo: it never reports extra links.
func hasOtherLinks(info os.FileInfo) bool {
	return false
}
