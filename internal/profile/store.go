package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrSchemaMismatch is returned by Load when store.json has an unsupported
// schema version.
var ErrSchemaMismatch = errors.New("store schema version mismatch")

// maxStoreSize limits how large store.json can be before we refuse to parse.
// Real-world stores are <10 KB even with 50 profiles; a multi-MB file is
// corruption or attack, not legitimate data.
const maxStoreSize = 1 << 20 // 1 MiB

// Load reads store.json from path and returns the parsed Store.
//
// Behavior:
//   - File does not exist → returns NewStore() (valid empty store). This is
//     the expected state on a fresh claudeorch install.
//   - File exists but is empty → returns error (corrupted state).
//   - File exists and has unsupported Version → returns ErrSchemaMismatch.
//   - File exists, valid JSON, valid version → returns populated Store with
//     Profile.Name populated from each map key.
//   - File > maxStoreSize → returns error (corruption guard).
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

	var s Store
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("profile.Load: parse %s: %w", path, err)
	}

	if s.Version != StoreVersion {
		return nil, fmt.Errorf("%w: %s has version %d, expected %d", ErrSchemaMismatch, path, s.Version, StoreVersion)
	}

	if s.Profiles == nil {
		s.Profiles = map[string]*Profile{}
	}
	// Rehydrate Profile.Name from the map key so in-memory profiles carry
	// their name without callers needing to pass it around separately.
	for name, p := range s.Profiles {
		if p == nil {
			return nil, fmt.Errorf("profile.Load: %s contains nil entry for %q", path, name)
		}
		p.Name = name
	}

	// Validate active pointer: must reference an existing profile (or be nil).
	if s.Active != nil {
		if _, ok := s.Profiles[*s.Active]; !ok {
			return nil, fmt.Errorf("profile.Load: %s has active=%q but no such profile", path, *s.Active)
		}
	}

	return &s, nil
}

// Save writes the store to path as JSON.
//
// Commit 4 scope: uses os.WriteFile with mode 0600. Commit 5 retrofits this
// to use internal/fsio WriteFileAtomic with proper temp+fsync+rename+parent-fsync.
// This non-atomic implementation is acceptable for the foundation layer
// because store.json is metadata (doctor can rebuild from profiles/ dir).
func (s *Store) Save(path string) error {
	if s == nil {
		return errors.New("profile.Store.Save: nil receiver")
	}
	if s.Version == 0 {
		s.Version = StoreVersion
	}
	if s.Version != StoreVersion {
		return fmt.Errorf("profile.Store.Save: refusing to write non-current version %d (expected %d)", s.Version, StoreVersion)
	}

	// Validate before writing — never persist an invalid store.
	for name, p := range s.Profiles {
		if p == nil {
			return fmt.Errorf("profile.Store.Save: nil profile for key %q", name)
		}
		if err := p.Validate(); err != nil {
			return fmt.Errorf("profile.Store.Save: profile %q invalid: %w", name, err)
		}
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("profile.Store.Save: marshal: %w", err)
	}
	// Append a trailing newline — makes file friendlier to cat/edit.
	data = append(data, '\n')

	// Ensure parent dir exists (0700).
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("profile.Store.Save: mkdir parent: %w", err)
	}

	// Non-atomic write (commit 5 upgrades this).
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("profile.Store.Save: write %s: %w", path, err)
	}
	return nil
}
