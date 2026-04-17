package profile

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	return v
}

func sampleProfile(name, email string) *Profile {
	return &Profile{
		Name:             name,
		Email:            email,
		OrganizationUUID: "org-uuid-" + name,
		OrganizationName: "Org " + name,
		CreatedAt:        time.Date(2026, 4, 17, 15, 0, 0, 0, time.UTC),
		Source:           SourceOAuth,
	}
}

// Empty store created via NewStore() should satisfy invariants.
func TestNewStore(t *testing.T) {
	s := NewStore()
	if s.Version != StoreVersion {
		t.Errorf("Version = %d, want %d", s.Version, StoreVersion)
	}
	if s.Active != nil {
		t.Errorf("Active = %v, want nil", s.Active)
	}
	if s.Profiles == nil {
		t.Errorf("Profiles = nil, want empty map")
	}
}

// Fresh Load of a missing file must return a valid empty store, NOT error.
// This matches the expected fresh-install experience.
func TestLoad_MissingFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "store.json")

	s, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if s == nil {
		t.Fatal("Load returned nil store")
	}
	if s.Version != StoreVersion {
		t.Errorf("Version = %d, want %d", s.Version, StoreVersion)
	}
	if len(s.Profiles) != 0 {
		t.Errorf("expected empty Profiles, got %d entries", len(s.Profiles))
	}
}

// Save → Load must round-trip a non-empty store exactly, with Profile.Name
// correctly re-populated from each map key.
func TestStore_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "store.json")

	original := NewStore()
	original.Profiles["work"] = sampleProfile("work", "alice@example.com")
	original.Profiles["home"] = sampleProfile("home", "alice@personal.dev")
	if err := original.SetActive("work"); err != nil {
		t.Fatal(err)
	}

	if err := original.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Version != StoreVersion {
		t.Errorf("Version = %d, want %d", loaded.Version, StoreVersion)
	}
	if loaded.Active == nil || *loaded.Active != "work" {
		t.Errorf("Active = %v, want \"work\"", loaded.Active)
	}
	if len(loaded.Profiles) != 2 {
		t.Fatalf("Profiles len = %d, want 2", len(loaded.Profiles))
	}
	for name, p := range loaded.Profiles {
		if p.Name != name {
			t.Errorf("Profile[%q].Name = %q, want matching key", name, p.Name)
		}
		if p.Email == "" || p.OrganizationUUID == "" {
			t.Errorf("Profile[%q] lost identity fields: %+v", name, p)
		}
	}
}

// The "version":1 field is load-bearing for future migrations and must
// always appear in the serialized JSON.
func TestSave_AlwaysIncludesVersion(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "store.json")

	s := NewStore()
	s.Profiles["work"] = sampleProfile("work", "alice@example.com")
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\"version\": 1") {
		t.Errorf("store.json missing version field:\n%s", string(data))
	}
}

// File permissions must be 0600 — credentials-adjacent metadata, don't leak.
func TestSave_Mode0600(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "store.json")

	s := NewStore()
	s.Profiles["work"] = sampleProfile("work", "alice@example.com")
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	mode := info.Mode().Perm()
	if mode != 0o600 {
		t.Errorf("store.json mode = %04o, want 0600", mode)
	}
}

// Missing "version" field → treat as unknown → error loudly.
func TestLoad_MissingVersion(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "store.json")

	rawJSON := `{"profiles": {}}`
	if err := os.WriteFile(path, []byte(rawJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if !errors.Is(err, ErrSchemaMismatch) {
		t.Errorf("Load err = %v, want ErrSchemaMismatch", err)
	}
}

// Unsupported version → error, never silent migration.
func TestLoad_UnsupportedVersion(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "store.json")

	rawJSON := `{"version": 99, "profiles": {}}`
	if err := os.WriteFile(path, []byte(rawJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if !errors.Is(err, ErrSchemaMismatch) {
		t.Errorf("Load err = %v, want ErrSchemaMismatch", err)
	}
}

// Malformed JSON → error with path context.
func TestLoad_MalformedJSON(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "store.json")

	if err := os.WriteFile(path, []byte(`not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error on malformed JSON, got nil")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error doesn't include path: %v", err)
	}
}

// Empty file → error (corrupted state, not fresh-install).
func TestLoad_EmptyFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "store.json")

	if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error on empty file")
	}
}

// Oversized file → error (corruption / attack guard).
func TestLoad_OversizedFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "store.json")

	huge := make([]byte, maxStoreSize+1)
	for i := range huge {
		huge[i] = 'a'
	}
	if err := os.WriteFile(path, huge, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error on oversized file")
	}
	if !strings.Contains(err.Error(), "exceeds max") {
		t.Errorf("expected 'exceeds max' in error, got: %v", err)
	}
}

// Active pointer to nonexistent profile → load error (consistency check).
func TestLoad_ActiveRefsUnknownProfile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "store.json")

	rawJSON := `{"version":1,"active":"ghost","profiles":{}}`
	if err := os.WriteFile(path, []byte(rawJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should name the missing profile: %v", err)
	}
}

// nil profile in map → loud failure.
func TestLoad_NilProfileEntry(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "store.json")

	rawJSON := `{"version":1,"profiles":{"work":null}}`
	if err := os.WriteFile(path, []byte(rawJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error on nil profile entry")
	}
}

func TestProfile_Validate_RejectsIncomplete(t *testing.T) {
	cases := []struct {
		name string
		p    *Profile
	}{
		{"nil profile", nil},
		{"missing email", &Profile{OrganizationUUID: "u", CreatedAt: time.Now(), Source: SourceOAuth}},
		{"missing org uuid", &Profile{Email: "e@e", CreatedAt: time.Now(), Source: SourceOAuth}},
		{"zero created_at", &Profile{Email: "e@e", OrganizationUUID: "u", Source: SourceOAuth}},
		{"unknown source", &Profile{Email: "e@e", OrganizationUUID: "u", CreatedAt: time.Now(), Source: "bogus"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.p.Validate(); err == nil {
				t.Errorf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

func TestProfile_Validate_Accepts(t *testing.T) {
	p := sampleProfile("work", "alice@example.com")
	if err := p.Validate(); err != nil {
		t.Errorf("unexpected err: %v", err)
	}
}

func TestSave_RefusesInvalidProfile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "store.json")

	s := NewStore()
	s.Profiles["bad"] = &Profile{} // missing all required fields
	err := s.Save(path)
	if err == nil {
		t.Fatal("expected error saving invalid profile")
	}
}

func TestSetActive(t *testing.T) {
	s := NewStore()
	s.Profiles["work"] = sampleProfile("work", "alice@example.com")

	// Clear (empty name) → nil pointer.
	if err := s.SetActive(""); err != nil {
		t.Fatal(err)
	}
	if s.Active != nil {
		t.Errorf("Active after clear = %v, want nil", s.Active)
	}

	// Set to existing profile.
	if err := s.SetActive("work"); err != nil {
		t.Fatal(err)
	}
	if s.Active == nil || *s.Active != "work" {
		t.Errorf("Active = %v, want \"work\"", s.Active)
	}

	// Set to unknown profile → error.
	if err := s.SetActive("ghost"); err == nil {
		t.Error("expected error for unknown profile")
	}
}

func TestIsActive(t *testing.T) {
	s := NewStore()
	s.Profiles["work"] = sampleProfile("work", "alice@example.com")
	_ = s.SetActive("work")

	if !s.IsActive("work") {
		t.Error("IsActive(\"work\") = false, want true")
	}
	if s.IsActive("home") {
		t.Error("IsActive(\"home\") = true, want false")
	}
}

// Sanity: serialized Profile JSON has expected fields and does NOT include Name.
func TestProfile_JSONShape(t *testing.T) {
	p := sampleProfile("work", "alice@example.com")
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)

	// Must include these:
	for _, k := range []string{"\"email\":", "\"organization_uuid\":", "\"source\":\"oauth\"", "\"created_at\":"} {
		if !strings.Contains(s, k) {
			t.Errorf("JSON missing %q: %s", k, s)
		}
	}
	// Must NOT include the Name field (it's a map key, not a value field).
	if strings.Contains(s, "\"Name\":") || strings.Contains(s, "\"name\":") {
		t.Errorf("JSON incorrectly includes Name field: %s", s)
	}
	// Must NOT include needs_reauth when false (omitempty).
	if strings.Contains(s, "\"needs_reauth\":") {
		t.Errorf("JSON incorrectly includes zero-value needs_reauth: %s", s)
	}
}

// Don't use mustTime in main code yet, but keep helper available.
var _ = mustTime
