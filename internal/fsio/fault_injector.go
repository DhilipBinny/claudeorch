//go:build faultinject

package fsio

import (
	"errors"
	"os"
	"sync"
)

// ErrInjectedFault is returned by the rename shim when fault injection fires.
var ErrInjectedFault = errors.New("fsio: injected fault")

var (
	faultMu        sync.Mutex
	faultCallsLeft int // -1 = disabled; 0 = fire on next call; >0 = countdown
	faultEnabled   bool
)

// SetRenameFailAfter arms the fault injector: the next n-th rename call
// (1-based) will return ErrInjectedFault.
//
//	SetRenameFailAfter(1) → fail the very next call
//	SetRenameFailAfter(2) → let one succeed, fail the second
func SetRenameFailAfter(n int) {
	faultMu.Lock()
	defer faultMu.Unlock()
	faultCallsLeft = n - 1
	faultEnabled = true
}

// ResetFaults disarms the fault injector. Safe to call when not armed.
func ResetFaults() {
	faultMu.Lock()
	defer faultMu.Unlock()
	faultEnabled = false
	faultCallsLeft = 0
}

// init replaces the production rename function with the injecting shim.
// The swap happens once at startup of any binary built with -tags=faultinject.
func init() {
	renameFunc = injectingRename
}

func injectingRename(oldpath, newpath string) error {
	faultMu.Lock()
	if faultEnabled {
		if faultCallsLeft <= 0 {
			faultEnabled = false
			faultMu.Unlock()
			return ErrInjectedFault
		}
		faultCallsLeft--
	}
	faultMu.Unlock()
	// Delegate to the real rename (not renameFunc — that would be recursive).
	return os.Rename(oldpath, newpath)
}
