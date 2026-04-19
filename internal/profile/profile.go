// Package profile defines the Profile type — claudeorch's saved representation
// of a Claude Code account — and the Store that indexes them.
//
// A Profile records identity metadata only (email, org UUID, timestamps,
// location). The actual OAuth credentials live on disk at
// ~/.claudeorch/profiles/<name>/credentials.json, NOT inside Profile or Store.
// This separation keeps the lightweight metadata index (store.json) free
// of secrets.
package profile

import (
	"errors"
	"fmt"
	"time"
)

// StoreVersion is the current schema version written to store.json.
//
// Versions:
//   - v1: Active *string at store level; no per-profile location.
//   - v2: Location per profile (dormant/live/isolated), TokensLastSeenAt per
//     profile. store.Active is computed in-memory from profiles with
//     Location == "live".
//
// Load() migrates v1 → v2 transparently. Save() always writes the current version.
const StoreVersion = 2

// Source identifies where a profile's credentials came from.
// v1 supports only OAuth (subscription login); API-key support may arrive in
// a later minor version.
type Source string

const (
	// SourceOAuth indicates credentials captured from `claude /login`.
	SourceOAuth Source = "oauth"
)

// ErrUnknownSource is returned when loading a profile with an unrecognized
// Source value.
var ErrUnknownSource = errors.New("unknown profile source")

// Validate returns nil if s is a known Source value.
func (s Source) Validate() error {
	switch s {
	case SourceOAuth:
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrUnknownSource, string(s))
	}
}

// Location describes where a profile's current-generation OAuth tokens
// are actively in use. Matters for OAuth refresh-token rotation safety:
// two processes holding the same refresh token are a ticking bomb —
// whoever refreshes first invalidates the other.
//
// Transitions (all driven by claudeorch commands):
//
//	dormant  -- swap N -->   live        (other live profile → dormant)
//	live     -- swap K -->   dormant     (K becomes live)
//	dormant  -- launch -->   isolated
//	isolated -- claude exits + next claudeorch cmd --> dormant
type Location string

const (
	// LocationDormant: tokens live only in ~/.claudeorch/profiles/<name>/.
	// Default for a freshly-added profile until its first swap or launch.
	LocationDormant Location = "dormant"

	// LocationLive: the profile's tokens are installed at ~/.claude/ and are
	// being actively rotated by Claude Code itself. At most ONE profile at a
	// time may be in this state.
	LocationLive Location = "live"

	// LocationIsolated: the profile's tokens are materialized at
	// ~/.claudeorch/isolate/<name>/ and a claude process is (or was) running
	// with CLAUDE_CONFIG_DIR pointed there. Multiple profiles may
	// simultaneously be isolated, but each identity only once.
	LocationIsolated Location = "isolated"
)

// ErrUnknownLocation is returned when loading a profile with an invalid Location.
var ErrUnknownLocation = errors.New("unknown profile location")

// Validate returns nil if l is a known Location value.
func (l Location) Validate() error {
	switch l {
	case LocationDormant, LocationLive, LocationIsolated:
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrUnknownLocation, string(l))
	}
}

// Profile is the saved metadata of a Claude Code account.
//
// The Name field is NOT serialized — it's the map key in Store.Profiles.
// Having it on the struct for in-memory use avoids passing (name, profile)
// pairs around everywhere.
type Profile struct {
	// Name is the user-chosen label (e.g., "work", "home"). Not serialized —
	// the map key in Store.Profiles is authoritative.
	Name string `json:"-"`

	// Email is oauthAccount.emailAddress from Claude Code's .claude.json.
	Email string `json:"email"`

	// OrganizationUUID identifies the Anthropic org this account belongs to.
	// Combined with Email forms the uniqueness key — the same email can
	// appear in multiple orgs.
	OrganizationUUID string `json:"organization_uuid"`

	// OrganizationName is a human-friendly label. May be empty for personal accounts.
	OrganizationName string `json:"organization_name"`

	// CreatedAt is when claudeorch added this profile (first `add` invocation).
	CreatedAt time.Time `json:"created_at"`

	// LastUsedAt is when this profile was last the subject of a command
	// (add/swap/launch/refresh). Zero = never used since creation.
	LastUsedAt time.Time `json:"last_used_at,omitempty"`

	// Source identifies credential origin. "oauth" in v1.
	Source Source `json:"source"`

	// NeedsReauth is set true when an OAuth refresh returned invalid_grant.
	// User must log in again via `claude /login` and re-add the profile.
	NeedsReauth bool `json:"needs_reauth,omitempty"`

	// Location tracks where this profile's tokens are currently in use.
	// See Location type docs for the state machine.
	Location Location `json:"location"`

	// TokensLastSeenAt is the expiresAt of the newest observed credentials
	// for this profile across {profile, live, isolate}. Updated by reconcile.
	// Used by doctor to detect stale profiles.
	TokensLastSeenAt time.Time `json:"tokens_last_seen_at,omitempty"`
}

// ErrMissingIdentity is returned by Validate when a required identity field is empty.
var ErrMissingIdentity = errors.New("profile missing required identity field")

// Validate returns nil if the profile has all required fields populated.
// Callers should validate before persisting to disk — never let an invalid
// profile reach store.json.
func (p *Profile) Validate() error {
	if p == nil {
		return errors.New("profile is nil")
	}
	if p.Email == "" {
		return fmt.Errorf("%w: email", ErrMissingIdentity)
	}
	if p.OrganizationUUID == "" {
		return fmt.Errorf("%w: organization_uuid", ErrMissingIdentity)
	}
	if p.CreatedAt.IsZero() {
		return fmt.Errorf("%w: created_at", ErrMissingIdentity)
	}
	if err := p.Source.Validate(); err != nil {
		return err
	}
	if err := p.Location.Validate(); err != nil {
		return err
	}
	return nil
}

// Store is the metadata index for all saved profiles.
// Persisted to ~/.claudeorch/store.json — not itself a secret store.
type Store struct {
	// Version is the schema version. Load() migrates older versions to
	// StoreVersion transparently; Save() always writes StoreVersion.
	Version int `json:"version"`

	// Active is the name of the profile whose Location == "live", or nil when
	// no profile is live. Computed from profiles on Load; NOT serialized
	// (the json:"-" tag omits it). Location per profile is the authoritative
	// source of truth.
	//
	// Retained in the in-memory API for convenience: most call sites ask
	// "who is live?" — this pointer answers directly.
	Active *string `json:"-"`

	// Profiles is keyed by profile name. The Profile.Name field is populated
	// from the map key on Load.
	Profiles map[string]*Profile `json:"profiles"`
}

// NewStore returns an empty store with the current schema version.
func NewStore() *Store {
	return &Store{
		Version:  StoreVersion,
		Active:   nil,
		Profiles: map[string]*Profile{},
	}
}

// SetActive marks the named profile as live and downgrades any previously-live
// profile to dormant. Empty name clears active (all profiles → dormant).
// Returns error if name is non-empty and does not exist in Profiles.
//
// Does NOT move files on disk — the caller is responsible for performing the
// actual swap before/after calling SetActive.
//
// Transition behaviour for the target profile (by its current Location):
//   - dormant → live: normal path (swap from another profile).
//   - live → live:    no-op.
//   - isolated → live: allowed but RARE. Represents a recovery where a
//     launched session no longer owns the isolate (see reconcile) and the
//     live credentials now belong to this profile. Callers that detect an
//     isolated-with-live-owner collision should refuse rather than calling
//     SetActive — reconcile does this.
func (s *Store) SetActive(name string) error {
	if name != "" {
		if _, ok := s.Profiles[name]; !ok {
			return fmt.Errorf("cannot set active to unknown profile %q", name)
		}
	}
	// Downgrade any currently-live profile.
	for _, p := range s.Profiles {
		if p.Location == LocationLive {
			p.Location = LocationDormant
		}
	}
	// Promote the target.
	if name == "" {
		s.Active = nil
		return nil
	}
	s.Profiles[name].Location = LocationLive
	n := name
	s.Active = &n
	return nil
}

// IsActive reports whether the named profile has Location == "live".
func (s *Store) IsActive(name string) bool {
	p, ok := s.Profiles[name]
	return ok && p.Location == LocationLive
}

// MarkIsolated sets the profile's Location to "isolated" without changing
// any other profile. Used by launch.
//
// Idempotent — calling on an already-isolated profile is a no-op and returns
// nil. Refuses on a currently-live profile (call SetActive("") first, or
// decide whether the current swap state is stale).
func (s *Store) MarkIsolated(name string) error {
	p, ok := s.Profiles[name]
	if !ok {
		return fmt.Errorf("unknown profile %q", name)
	}
	if p.Location == LocationIsolated {
		return nil // already isolated, idempotent no-op
	}
	if p.Location == LocationLive {
		return fmt.Errorf("cannot mark %q isolated: currently live", name)
	}
	p.Location = LocationIsolated
	return nil
}

// MarkDormant sets the profile's Location to "dormant". Used when a launch
// session ends (detected by reconcile) or on explicit reset.
func (s *Store) MarkDormant(name string) error {
	p, ok := s.Profiles[name]
	if !ok {
		return fmt.Errorf("unknown profile %q", name)
	}
	p.Location = LocationDormant
	if s.Active != nil && *s.Active == name {
		s.Active = nil
	}
	return nil
}
