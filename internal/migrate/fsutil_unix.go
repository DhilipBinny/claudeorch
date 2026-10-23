//go:build !windows

package migrate

import (
	"os"
	"syscall"
)

// openNoFollow makes opening a path fail if its final component is a
// symlink, closing the window between WalkDir's type check and the open.
const openNoFollow = syscall.O_NOFOLLOW

// hasOtherLinks reports whether a file has more than one hard link. Zeroing
// such a file would also destroy the data seen through its other names.
func hasOtherLinks(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Nlink > 1
}
