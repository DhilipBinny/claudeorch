package main

import (
	"fmt"
	"io"

	"github.com/DhilipBinny/claudeorch/internal/paths"
	"github.com/DhilipBinny/claudeorch/internal/profile"
	"github.com/DhilipBinny/claudeorch/internal/reconcile"
)

// reconcileProfiles runs the freshest-wins sync pass on the given store.
//
// Contract:
//   - Callers must already hold the global flock (~/.claudeorch/locks/.lock).
//     Every state-mutating command holds it; 'sync' acquires it explicitly.
//   - The returned Report describes what changed; the caller is responsible
//     for saving the store afterwards (changes are in-memory only here).
//   - User-facing warnings (conflicts, unknown identity, duplicates) are
//     printed to warnOut as a side-effect — they're informational and don't
//     abort the caller's flow.
//
// Errors are rare (disk write failures on credential promotion); callers
// should surface them.
func reconcileProfiles(store *profile.Store, warnOut io.Writer) (reconcile.Report, error) {
	rPaths, err := buildReconcilePaths()
	if err != nil {
		return reconcile.Report{}, fmt.Errorf("resolve paths for reconcile: %w", err)
	}
	rep, err := reconcile.Reconcile(store, rPaths)
	if err != nil {
		return rep, err
	}
	emitReconcileWarnings(warnOut, rep)
	return rep, nil
}

// buildReconcilePaths resolves the filesystem roots reconcile needs. Returns
// an error only when HOME is unset (claudeorch is broken at that point).
func buildReconcilePaths() (reconcile.Paths, error) {
	claudeHome, err := paths.ClaudeConfigHome()
	if err != nil {
		return reconcile.Paths{}, err
	}
	claudeJSON, err := paths.ClaudeJSONPath()
	if err != nil {
		return reconcile.Paths{}, err
	}
	isolates, err := paths.IsolatesRoot()
	if err != nil {
		return reconcile.Paths{}, err
	}
	profiles, err := paths.ProfilesRoot()
	if err != nil {
		return reconcile.Paths{}, err
	}
	return reconcile.Paths{
		ClaudeConfigHome: claudeHome,
		ClaudeJSONPath:   claudeJSON,
		IsolatesRoot:     isolates,
		ProfilesRoot:     profiles,
	}, nil
}

// emitReconcileWarnings prints user-facing advisories for anything in the
// report that needs user attention. Silent when the report is clean.
func emitReconcileWarnings(w io.Writer, rep reconcile.Report) {
	for _, name := range rep.IsolatedLiveConflicts {
		fmt.Fprintf(w, "\nWarning: profile %q is both launched (isolate session running)\n", name)
		fmt.Fprintf(w, "  AND live (~/.claude/ holds the same identity). OAuth refresh-token\n")
		fmt.Fprintf(w, "  rotation will eventually invalidate one side. Close the launched\n")
		fmt.Fprintf(w, "  claude session, or accept that one of them will hit 401 shortly.\n")
	}
	if rep.LiveIdentityUnknown {
		fmt.Fprintf(w, "\nNote: ~/.claude/ holds an identity not yet saved in claudeorch.\n")
		fmt.Fprintf(w, "  Run 'claudeorch add <name>' to register it.\n")
	}
	for _, dup := range rep.DuplicateIdentities {
		fmt.Fprintf(w, "\nWarning: duplicate identity in store — %s\n", dup)
		fmt.Fprintf(w, "  This shouldn't happen via 'add'; it implies a manual edit or a bug.\n")
	}
}
