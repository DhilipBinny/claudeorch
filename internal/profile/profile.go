// Package profile defines the Profile type — claudeorch's saved representation
// of a Claude Code account — and the Store that indexes them.
//
// A Profile records identity metadata only (email, org UUID, timestamps).
// The actual OAuth credentials live on disk at
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
// Incremented when the schema changes in a non-backwards-compatible way.
const StoreVersion = 1

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

// Profile is the saved identity of a Claude Code account.
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
	return nil
}

// Store is the metadata index for all saved profiles.
// Persisted to ~/.claudeorch/store.json — not itself a secret store.
type Store struct {
	// Version is the schema version. Must equal StoreVersion on load;
	// mismatches are errors (we don't silently migrate).
	Version int `json:"version"`

	// Active is the name of the profile currently installed at ~/.claude/
	// (swap mode). nil when no profile is active (fresh install or after
	// explicit removal of the active profile).
	//
	// Pointer + omitempty gives us explicit null in JSON rather than "".
	Active *string `json:"active,omitempty"`

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

// SetActive marks the named profile as active (or clears active if name is empty).
// Returns error if the name is non-empty but doesn't exist in Profiles.
func (s *Store) SetActive(name string) error {
	if name == "" {
		s.Active = nil
		return nil
	}
	if _, ok := s.Profiles[name]; !ok {
		return fmt.Errorf("cannot set active to unknown profile %q", name)
	}
	n := name
	s.Active = &n
	return nil
}

// IsActive reports whether the named profile is currently the active one.
func (s *Store) IsActive(name string) bool {
	return s.Active != nil && *s.Active == name
}
