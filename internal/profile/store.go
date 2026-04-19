package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DhilipBinny/claudeorch/internal/fsio"
)

// ErrSchemaMismatch is returned by Load when store.json has a schema version
// that can't be migrated to StoreVersion.
var ErrSchemaMismatch = errors.New("store schema version mismatch")

// maxStoreSize limits how large store.json can be before we refuse to parse.
// Real-world stores are <10 KB even with 50 profiles; a multi-MB file is
// corruption or attack, not legitimate data.
const maxStoreSize = 1 << 20 // 1 MiB

// Load reads store.json from path and returns the parsed Store, migrating
// older schema versions to StoreVersion transparently in-memory.
//
// Behaviour:
//   - File does not exist → NewStore() (valid empty store; fresh install).
//   - File exists but empty → error (corrupted state).
//   - File exists, version > StoreVersion → ErrSchemaMismatch (forward
//     compat: don't silently drop fields from a newer file).
//   - File exists, version == 1 → migrated to v2 (all profiles default to
//     "dormant"; the previously-active profile, if any, becomes "live").
//   - File exists, version == 2 → returned as-is with Active computed.
//   - File > maxStoreSize → error (corruption guard).
//
// Nil store is never returned; callers don't need to nil-check.
func Load(path string) (*Store, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return NewStore(), nil
		}
		return nil, fmt.Errorf("profile.Load: stat %s: %w", path, err)
	}

	if info.Size() == 0 {
		return nil, fmt.Errorf("profile.Load: %s is empty (corrupted state)", path)
	}
	if info.Size() > maxStoreSize {
		return nil, fmt.Errorf("profile.Load: %s is %d bytes, exceeds max %d (corrupted or unsafe)", path, info.Size(), maxStoreSize)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("profile.Load: read %s: %w", path, err)
	}

	// rawStore captures fields from any supported version. v1 had a top-level
	// Active pointer; v2 does not (Location per profile is authoritative).
	var raw struct {
		Version  int                 `json:"version"`
		Active   *string             `json:"active,omitempty"` // v1 only
		Profiles map[string]*Profile `json:"profiles"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("profile.Load: parse %s: %w", path, err)
	}

	if raw.Version > StoreVersion {
		return nil, fmt.Errorf("%w: %s has version %d, this binary supports up to %d (upgrade claudeorch)", ErrSchemaMismatch, path, raw.Version, StoreVersion)
	}
	if raw.Version < 1 {
		return nil, fmt.Errorf("%w: %s has invalid version %d", ErrSchemaMismatch, path, raw.Version)
	}

	if raw.Profiles == nil {
		raw.Profiles = map[string]*Profile{}
	}

	s := &Store{
		Version:  StoreVersion,
		Profiles: raw.Profiles,
	}

	// Rehydrate Profile.Name from the map key.
	for name, p := range s.Profiles {
		if p == nil {
			return nil, fmt.Errorf("profile.Load: %s contains nil entry for %q", path, name)
		}
		p.Name = name
	}

	// Version-specific migration. After this block, every profile has a valid
	// Location and Store.Active is computed.
	switch raw.Version {
	case 1:
		// v1 had no per-profile Location. Default everyone to dormant, then
		// promote the previously-active profile to live.
		for _, p := range s.Profiles {
			p.Location = LocationDormant
		}
		if raw.Active != nil {
			target, ok := s.Profiles[*raw.Active]
			if !ok {
				return nil, fmt.Errorf("profile.Load: v1 active=%q but no such profile", *raw.Active)
			}
			target.Location = LocationLive
			n := *raw.Active
			s.Active = &n
		}
	case 2:
		// v2: Location is serialized. Validate + compute Active from it.
		for _, p := range s.Profiles {
			if p.Location == "" {
				// Tolerate missing field in older-within-v2 files — default to dormant.
				p.Location = LocationDormant
			}
			if err := p.Location.Validate(); err != nil {
				return nil, fmt.Errorf("profile.Load: profile %q: %w", p.Name, err)
			}
		}
		if err := computeActive(s); err != nil {
			return nil, fmt.Errorf("profile.Load: %s: %w", path, err)
		}
	default:
		return nil, fmt.Errorf("%w: %s has unsupported version %d", ErrSchemaMismatch, path, raw.Version)
	}

	return s, nil
}

// computeActive finds the one profile whose Location == "live" and sets
// Store.Active to point at its name. Returns an error if more than one
// profile is marked live (corruption — should be impossible via the API).
func computeActive(s *Store) error {
	var liveName string
	for name, p := range s.Profiles {
		if p.Location == LocationLive {
			if liveName != "" {
				return fmt.Errorf("profiles %q and %q are both marked live (corruption)", liveName, name)
			}
			liveName = name
		}
	}
	if liveName == "" {
		s.Active = nil
		return nil
	}
	s.Active = &liveName
	return nil
}

// Save writes the store to path as JSON atomically (temp+fsync+rename).
// Always writes the current StoreVersion; the in-memory Active pointer is
// NOT serialized (it's derived from per-profile Location).
func (s *Store) Save(path string) error {
	if s == nil {
		return errors.New("profile.Store.Save: nil receiver")
	}
	// Always upgrade to current version on save.
	s.Version = StoreVersion

	// Sanity: at most one profile may be live.
	liveCount := 0
	for _, p := range s.Profiles {
		if p == nil {
			return fmt.Errorf("profile.Store.Save: nil profile in store")
		}
		if p.Location == LocationLive {
			liveCount++
		}
		if err := p.Validate(); err != nil {
			return fmt.Errorf("profile.Store.Save: profile %q invalid: %w", p.Name, err)
		}
	}
	if liveCount > 1 {
		return fmt.Errorf("profile.Store.Save: %d profiles marked live (at most 1 allowed)", liveCount)
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("profile.Store.Save: marshal: %w", err)
	}
	// Append a trailing newline — makes file friendlier to cat/edit.
	data = append(data, '\n')

	// Ensure parent dir exists (0700).
	if err := fsio.EnsureDir(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("profile.Store.Save: %w", err)
	}

	if err := fsio.WriteFileAtomic(path, data, 0o600); err != nil {
		return fmt.Errorf("profile.Store.Save: %w", err)
	}
	return nil
}
