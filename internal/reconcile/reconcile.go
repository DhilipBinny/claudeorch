// Package reconcile implements the freshest-wins sync pass that keeps
// profile credentials current when Claude Code silently rotates tokens
// via OAuth 2.0 refresh-token rotation.
//
// Problem: when Claude Code refreshes an access token, the server
// simultaneously issues a new refresh token AND invalidates the old one.
// Any saved copy (a `profile/<name>/credentials.json` snapshot) instantly
// becomes stale. Within a few hours of normal usage, all saved profiles
// hold revoked refresh tokens.
//
// Solution: every claudeorch command that could be affected runs Reconcile
// first. It scans the three locations where a profile's tokens might live
// (profile, isolate, live ~/.claude/ when identity matches), picks the
// newest by expiresAt, and promotes it to the canonical profile location.
// Orphan isolate sessions (dead claude PIDs) are cleaned up; drift in the
// Active pointer is corrected.
//
// The invariant Reconcile enforces:
//
//	profile/<name>/credentials.json holds the most-recently-issued
//	refresh token for <name> that the local machine knows about.
package reconcile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DhilipBinny/claudeorch/internal/creds"
	"github.com/DhilipBinny/claudeorch/internal/fsio"
	"github.com/DhilipBinny/claudeorch/internal/profile"
	"github.com/DhilipBinny/claudeorch/internal/schema"
	"github.com/DhilipBinny/claudeorch/internal/session"
)

// Paths groups the filesystem roots reconcile needs.
type Paths struct {
	// ClaudeConfigHome is ~/.claude/ (or $CLAUDE_CONFIG_DIR).
	ClaudeConfigHome string
	// ClaudeJSONPath is ~/.claude.json (or inside config dir when
	// CLAUDE_CONFIG_DIR is set — this is the one with the asymmetric path).
	ClaudeJSONPath string
	// IsolatesRoot is ~/.claudeorch/isolate/.
	IsolatesRoot string
	// ProfilesRoot is ~/.claudeorch/profiles/.
	ProfilesRoot string
}

// Report summarises what Reconcile changed.
type Report struct {
	// TokensPromoted lists profile names whose credentials were replaced
	// with fresher tokens from live or isolate.
	TokensPromoted []string
	// OrphansCleared lists profile names whose Location was changed from
	// "isolated" to "dormant" because no claude process owned the isolate.
	OrphansCleared []string
	// ActiveCorrected is true when the store's Active pointer was changed
	// to reflect reality (e.g., user logged in elsewhere).
	ActiveCorrected bool
	// LiveIdentityUnknown is true when live ~/.claude/ holds an identity
	// that no saved profile matches. Useful for user-facing warnings.
	LiveIdentityUnknown bool
	// DuplicateIdentities is the list of (email, org_uuid) tuples that
	// appear on more than one profile. Populated only when such corruption
	// is detected — normally empty. The `add` command prevents this; a
	// populated list implies a manual edit of store.json or a bug elsewhere.
	DuplicateIdentities []string
	// IsolatedLiveConflicts lists profile names that are currently "isolated"
	// (a launched claude session owns their isolate dir) AND also match the
	// live ~/.claude/ identity. This is the dangerous double-holder state
	// that OAuth refresh-token rotation punishes: both the launched session
	// and the live process think they have the valid tokens, and whichever
	// refreshes first invalidates the other. Reconcile surfaces this WITHOUT
	// auto-resolving — the user has to kill the launched session or choose
	// which side to keep.
	IsolatedLiveConflicts []string
}

// Changed reports whether reconcile made any observable change.
func (r Report) Changed() bool {
	return len(r.TokensPromoted) > 0 ||
		len(r.OrphansCleared) > 0 ||
		r.ActiveCorrected
}

// detectDuplicateIdentities returns a human-friendly description of any
// (email, org_uuid) tuples shared by more than one profile. `add` prevents
// this, so a non-empty result implies a bug or a manual edit.
func detectDuplicateIdentities(s *profile.Store) []string {
	seen := map[string][]string{}
	for name, p := range s.Profiles {
		key := p.Email + "|" + p.OrganizationUUID
		seen[key] = append(seen[key], name)
	}
	var dupes []string
	for k, names := range seen {
		if len(names) > 1 {
			dupes = append(dupes, k+" → "+joinNames(names))
		}
	}
	return dupes
}

func joinNames(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}

// credSource describes one candidate location for a profile's tokens.
type credSource struct {
	label string // "profile" / "isolate" / "live"
	path  string
	creds *schema.Credentials // nil when path doesn't exist or is unreadable
}

// Reconcile runs the freshest-wins sync + state correction pass across
// every profile in the store. Modifies the store in place. Callers must
// Save() the store after a successful call for the changes to persist.
//
// # Locking contract
//
// Callers MUST hold the global claudeorch flock (~/.claudeorch/locks/.lock)
// before invoking Reconcile. Without the lock, two concurrent claudeorch
// processes could both promote different sources and race on the file write,
// and the returned store could disagree with what's actually on disk.
//
// Every state-mutating command already takes the flock before calling
// Reconcile, so this is normally invisible. The `sync` subcommand is the
// only user-facing one that explicitly exists for reconcile; it takes the
// lock too.
//
// # Errors
//
// Returns a Report describing what changed. An empty Report means reality
// already matched the store; nothing to do.
//
// Errors are rare and fatal — e.g., unable to write a promoted credential
// file. Reconcile skips individual unreadable files silently because they
// may be transient (concurrent claude refresh mid-write).
func Reconcile(store *profile.Store, p Paths) (Report, error) {
	var rep Report
	if store == nil {
		return rep, errors.New("reconcile: nil store")
	}

	// Defensive: flag duplicate (email, org_uuid) identities. The `add`
	// command already refuses to create these, but a corrupted store or
	// manual edit could produce them — without this guard, the first-match
	// logic in reconcileActivePointer would pick an arbitrary profile via
	// Go's randomised map iteration. We pick the FIRST in a stable order
	// (lexicographic by name) so behaviour is at least deterministic.
	rep.DuplicateIdentities = detectDuplicateIdentities(store)

	// Step 1: sniff the live ~/.claude/ identity so we know which profile
	// (if any) is currently "live".
	liveIdentity := sniffLiveIdentity(p.ClaudeJSONPath)

	// Step 2: per-profile reconciliation (token freshness + orphan cleanup).
	for _, prof := range store.Profiles {
		if err := reconcileOne(prof, store, p, liveIdentity, &rep); err != nil {
			return rep, err
		}
	}

	// Step 3: correct Active pointer drift.
	if err := reconcileActivePointer(store, liveIdentity, &rep); err != nil {
		return rep, err
	}

	return rep, nil
}

// reconcileOne handles a single profile's candidates + location state.
func reconcileOne(prof *profile.Profile, store *profile.Store, p Paths,
	liveIdentity *schema.Identity, rep *Report) error {

	profileCredsPath := filepath.Join(p.ProfilesRoot, prof.Name, "credentials.json")
	isolateCredsPath := filepath.Join(p.IsolatesRoot, prof.Name, ".credentials.json")
	liveCredsPath := filepath.Join(p.ClaudeConfigHome, ".credentials.json")

	sources := []credSource{
		{label: "profile", path: profileCredsPath, creds: readCreds(profileCredsPath)},
	}

	if _, err := os.Stat(isolateCredsPath); err == nil {
		sources = append(sources, credSource{
			label: "isolate", path: isolateCredsPath, creds: readCreds(isolateCredsPath),
		})
	}

	// Only consider live as a candidate if its identity matches THIS profile.
	// On macOS, live credentials are in the Keychain (not at liveCredsPath),
	// so we use creds.ReadLive which tries Keychain first, then flat file.
	if liveIdentity != nil &&
		liveIdentity.EmailAddress == prof.Email &&
		liveIdentity.OrganizationUUID == prof.OrganizationUUID {
		if lc := readLiveCreds(liveCredsPath); lc != nil {
			sources = append(sources, credSource{
				label: "live", path: liveCredsPath, creds: lc,
			})
		}
	}

	// Pick the newest by expiresAt.
	freshest := pickFreshest(sources)

	// Promote if freshest isn't already the profile copy (and is readable).
	if freshest != nil && freshest.label != "profile" && freshest.creds != nil {
		if err := copyCreds(freshest.path, profileCredsPath); err != nil {
			return fmt.Errorf("reconcile: promote %s → %s: %w",
				freshest.label, prof.Name, err)
		}
		rep.TokensPromoted = append(rep.TokensPromoted, prof.Name)
		prof.TokensLastSeenAt = freshest.creds.ExpiresAt
	} else if freshest != nil && freshest.creds != nil {
		// No promotion needed, but update TokensLastSeenAt anyway.
		prof.TokensLastSeenAt = freshest.creds.ExpiresAt
	}

	// Orphan-isolate detection: if Location=="isolated" but no live claude
	// owns the isolate dir, downgrade to dormant.
	if prof.Location == profile.LocationIsolated {
		if !isolateHasLiveOwner(filepath.Join(p.IsolatesRoot, prof.Name)) {
			prof.Location = profile.LocationDormant
			rep.OrphansCleared = append(rep.OrphansCleared, prof.Name)
		}
	}

	return nil
}

// reconcileActivePointer corrects the store's Active pointer when reality
// has drifted (e.g., user did `claude /login` outside claudeorch).
func reconcileActivePointer(store *profile.Store, liveIdentity *schema.Identity,
	rep *Report) error {

	if liveIdentity == nil {
		// No live identity (no ~/.claude.json or unreadable). Anything
		// currently marked "live" in the store is stale.
		for _, prof := range store.Profiles {
			if prof.Location == profile.LocationLive {
				prof.Location = profile.LocationDormant
				rep.ActiveCorrected = true
			}
		}
		store.Active = nil
		return nil
	}

	// Find the profile matching live identity, if any. If (defensively)
	// multiple profiles match, pick the lexicographically smallest name
	// so the choice is deterministic across runs — map iteration in Go
	// is intentionally randomised.
	var liveProfileName string
	for name, prof := range store.Profiles {
		if prof.Email == liveIdentity.EmailAddress &&
			prof.OrganizationUUID == liveIdentity.OrganizationUUID {
			if liveProfileName == "" || name < liveProfileName {
				liveProfileName = name
			}
		}
	}

	if liveProfileName == "" {
		// Live identity doesn't match any saved profile (user logged in as
		// an unknown account). Downgrade any previously-live profile.
		for _, prof := range store.Profiles {
			if prof.Location == profile.LocationLive {
				prof.Location = profile.LocationDormant
				rep.ActiveCorrected = true
			}
		}
		store.Active = nil
		rep.LiveIdentityUnknown = true
		return nil
	}

	// Danger zone: the matching profile is currently "isolated" AND a live
	// claude process still owns the isolate dir (reconcileOne would have
	// already downgraded it to dormant if orphaned). This means the same
	// account's refresh token is simultaneously held by:
	//   - The launched claude session, using isolate/<name>/.credentials.json
	//   - The live ~/.claude/ session that matches this identity
	// Whichever process refreshes first invalidates the other. We must NOT
	// silently promote this to "live" and call it done — instead surface the
	// conflict for the user to resolve.
	if store.Profiles[liveProfileName].Location == profile.LocationIsolated {
		rep.IsolatedLiveConflicts = append(rep.IsolatedLiveConflicts, liveProfileName)
		// Leave Active + Location as-is; don't make the state worse.
		return nil
	}

	// Apply the correct "live" pointer. SetActive handles downgrading
	// any previously-live profile.
	if store.Active == nil || *store.Active != liveProfileName {
		// Only flag as corrected if the state actually changed.
		if err := store.SetActive(liveProfileName); err != nil {
			return fmt.Errorf("reconcile: SetActive(%q): %w", liveProfileName, err)
		}
		rep.ActiveCorrected = true
	} else if store.Profiles[liveProfileName].Location != profile.LocationLive {
		// Active pointer was correct but Location wasn't — repair.
		if err := store.SetActive(liveProfileName); err != nil {
			return err
		}
		rep.ActiveCorrected = true
	}
	return nil
}

// sniffLiveIdentity returns the identity from ~/.claude.json, or nil when
// the file is missing, unreadable, or malformed. Never errors — caller
// handles the nil case as "no live account".
func sniffLiveIdentity(path string) *schema.Identity {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	id, err := schema.ExtractIdentity(data)
	if err != nil {
		return nil
	}
	return id
}

// readCreds reads and parses a credentials.json from a flat file.
// Returns nil on any error (missing, unreadable, malformed) — transient
// failures during concurrent Claude refresh should not blow up reconcile.
// Used for profile/ and isolate/ sources, which are always flat files.
func readCreds(path string) *schema.Credentials {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	c, err := schema.ParseCredentials(data)
	if err != nil {
		return nil
	}
	return c
}

// readLiveCreds reads Claude Code's live credentials using the platform-
// aware creds package: flat file on Linux, Keychain on macOS. Returns
// nil on any error (same defensive behaviour as readCreds).
func readLiveCreds(credsPath string) *schema.Credentials {
	data, err := creds.ReadLive(credsPath)
	if err != nil {
		return nil
	}
	c, err := schema.ParseCredentials(data)
	if err != nil {
		return nil
	}
	return c
}

// pickFreshest returns the candidate with the latest ExpiresAt, preferring
// the "profile" label on ties so we don't churn the filesystem needlessly.
func pickFreshest(sources []credSource) *credSource {
	var best *credSource
	for i := range sources {
		c := &sources[i]
		if c.creds == nil {
			continue
		}
		if best == nil {
			best = c
			continue
		}
		if c.creds.ExpiresAt.After(best.creds.ExpiresAt) {
			best = c
			continue
		}
		// Tie-break: prefer "profile" (stays authoritative, avoids needless copy).
		if c.creds.ExpiresAt.Equal(best.creds.ExpiresAt) && c.label == "profile" {
			best = c
		}
	}
	return best
}

// copyCreds atomically copies src → dst at mode 0600. Matches the pattern
// used elsewhere (fsio.WriteFileAtomic with the source bytes).
func copyCreds(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := fsio.EnsureDir(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	return fsio.WriteFileAtomic(dst, data, 0o600)
}

// isolateHasLiveOwner reports whether any running process has its
// CLAUDE_CONFIG_DIR set to isolateDir. Checks /proc/*/environ on Linux;
// returns true on macOS (where we can't read other processes' env) to
// avoid false-positive orphan reports.
func isolateHasLiveOwner(isolateDir string) bool {
	// List all process PIDs and inspect their CLAUDE_CONFIG_DIR.
	entries, err := os.ReadDir("/proc")
	if err != nil {
		// Not Linux (or /proc unreadable) — be conservative: assume owned,
		// so we don't wrongly mark isolates as orphaned.
		return true
	}
	target := filepath.Clean(isolateDir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid := atoiOrZero(e.Name())
		if pid <= 0 {
			continue
		}
		envDir := session.ConfigDirForPID(pid)
		if envDir == "" {
			continue
		}
		if filepath.Clean(envDir) == target {
			return true
		}
	}
	return false
}

func atoiOrZero(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
