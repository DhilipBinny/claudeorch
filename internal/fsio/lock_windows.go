//go:build windows

package fsio

import (
	"fmt"
	"os"
	"time"
)

// acquireExclusiveFlock is a stub for Windows. Windows support is Phase 2.
func acquireExclusiveFlock(_ *os.File, _ time.Time) error {
	return fmt.Errorf("fsio: file locking not supported on Windows (Phase 2)")
}

// releaseFlock is a stub for Windows.
func releaseFlock(_ *os.File) error {
	return fmt.Errorf("fsio: file locking not supported on Windows (Phase 2)")
}
