package reconcile

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/DhilipBinny/claudeorch/internal/profile"
)

// Helper: write a credentials.json with expiresAt at the given future hours.
// Also uses matching email/org to be able to compare identity.
func writeCreds(t *testing.T, path string, accessToken, refreshToken string, expiresHoursFromNow int) {
	t.Helper()
	expiresMs := time.Now().Add(time.Duration(expiresHoursFromNow)*time.Hour).UnixMilli()
	payload := map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken":  accessToken,
			"refreshToken": refreshToken,
			"expiresAt":    expiresMs,
			"scopes":       []string{"openai"},
		},
	}
	data, _ := json.Marshal(payload)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeClaudeJSON(t *testing.T, path, email, orgUUID string) {
	t.Helper()
	payload := map[string]any{
		"oauthAccount": map[string]any{
			"emailAddress":     email,
			"organizationUuid": orgUUID,
			"organizationName": email + "'s org",
		},
	}
	data, _ := json.Marshal(payload)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// makeEnv sets up a temp paths struct + empty profiles/isolate roots.
func makeEnv(t *testing.T) Paths {
	t.Helper()
	root := t.TempDir()
	return Paths{
		ClaudeConfigHome: filepath.Join(root, ".claude"),
		ClaudeJSONPath:   filepath.Join(root, ".claude.json"),
		IsolatesRoot:     filepath.Join(root, "orch", "isolate"),
		ProfilesRoot:     filepath.Join(root, "orch", "profiles"),
	}
}

func sampleProfile(name, email string) *profile.Profile {
	return &profile.Profile{
		Name:             name,
		Email:            email,
		OrganizationUUID: "org-" + name,
		OrganizationName: "Org " + name,
		CreatedAt:        time.Date(2026, 4, 17, 15, 0, 0, 0, time.UTC),
		Source:           profile.SourceOAuth,
		Location:         profile.LocationDormant,
	}
}

func TestReconcile_NilStore(t *testing.T) {
	_, err := Reconcile(nil, Paths{})
	if err == nil {
		t.Error("expected error for nil store")
	}
}

func TestReconcile_EmptyStore_NoChange(t *testing.T) {
	p := makeEnv(t)
	s := profile.NewStore()
	rep, err := Reconcile(s, p)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Changed() {
		t.Errorf("empty store should not report changes: %+v", rep)
	}
}

// Live has fresher credentials than profile → reconcile promotes live → profile.
func TestReconcile_PromoteLiveToProfile_WhenLiveMatchesIdentity(t *testing.T) {
	p := makeEnv(t)
	s := profile.NewStore()
	s.Profiles["work"] = sampleProfile("work", "a@x.com")
	_ = s.SetActive("work")

	// Profile creds: stale (expired 1h ago).
	profileCreds := filepath.Join(p.ProfilesRoot, "work", "credentials.json")
	writeCreds(t, profileCreds, "stale_access", "stale_refresh", -1)

	// Live creds: fresh (expires in 1h), matching identity.
	writeClaudeJSON(t, p.ClaudeJSONPath, "a@x.com", "org-work")
	liveCreds := filepath.Join(p.ClaudeConfigHome, ".credentials.json")
	writeCreds(t, liveCreds, "fresh_access", "fresh_refresh", 1)

	rep, err := Reconcile(s, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.TokensPromoted) != 1 || rep.TokensPromoted[0] != "work" {
		t.Errorf("TokensPromoted = %v, want [work]", rep.TokensPromoted)
	}
	// Profile creds should now contain the fresh token.
	data, _ := os.ReadFile(profileCreds)
	if !contains(string(data), "fresh_access") {
		t.Errorf("profile creds not promoted: %s", data)
	}
}

// Profile is already freshest → no promotion.
func TestReconcile_NoChange_WhenProfileIsFreshest(t *testing.T) {
	p := makeEnv(t)
	s := profile.NewStore()
	s.Profiles["work"] = sampleProfile("work", "a@x.com")
	_ = s.SetActive("work")

	// Profile creds: fresh.
	profileCreds := filepath.Join(p.ProfilesRoot, "work", "credentials.json")
	writeCreds(t, profileCreds, "fresh", "fresh_r", 2)

	// Live creds: older.
	writeClaudeJSON(t, p.ClaudeJSONPath, "a@x.com", "org-work")
	liveCreds := filepath.Join(p.ClaudeConfigHome, ".credentials.json")
	writeCreds(t, liveCreds, "older", "older_r", 1)

	rep, err := Reconcile(s, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.TokensPromoted) != 0 {
		t.Errorf("should not promote when profile is freshest: %v", rep.TokensPromoted)
	}
}

// Live identity does NOT match profile → live is NOT considered a candidate.
func TestReconcile_LiveIdentityMismatch_NotPromoted(t *testing.T) {
	p := makeEnv(t)
	s := profile.NewStore()
	s.Profiles["work"] = sampleProfile("work", "work@x.com")
	_ = s.SetActive("work")

	profileCreds := filepath.Join(p.ProfilesRoot, "work", "credentials.json")
	writeCreds(t, profileCreds, "profile_tok", "profile_r", 1)

	// Live is a different account — should not be considered as source for work.
	writeClaudeJSON(t, p.ClaudeJSONPath, "someone_else@x.com", "org-other")
	liveCreds := filepath.Join(p.ClaudeConfigHome, ".credentials.json")
	writeCreds(t, liveCreds, "other_tok", "other_r", 99)

	rep, err := Reconcile(s, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.TokensPromoted) != 0 {
		t.Errorf("should not pull live tokens of different identity: %v", rep.TokensPromoted)
	}
	// work profile's creds stay as profile_tok.
	data, _ := os.ReadFile(profileCreds)
	if !contains(string(data), "profile_tok") {
		t.Errorf("profile creds wrongly replaced: %s", data)
	}
	// Work was previously "live" in store, but live is a different identity.
	// Reconcile should downgrade it to dormant.
	if s.Profiles["work"].Location != profile.LocationDormant {
		t.Errorf("work.Location = %q, want dormant (live belongs to another identity)",
			s.Profiles["work"].Location)
	}
	if !rep.ActiveCorrected {
		t.Errorf("ActiveCorrected should be true")
	}
	if !rep.LiveIdentityUnknown {
		t.Errorf("LiveIdentityUnknown should be true (unknown identity)")
	}
}

// Isolate has fresher tokens (a previous launch session rotated them) →
// reconcile promotes isolate → profile. This is the exact bug v0.1.0 had.
func TestReconcile_PromoteIsolateToProfile(t *testing.T) {
	p := makeEnv(t)
	s := profile.NewStore()
	s.Profiles["work"] = sampleProfile("work", "a@x.com")
	s.Profiles["work"].Location = profile.LocationIsolated

	profileCreds := filepath.Join(p.ProfilesRoot, "work", "credentials.json")
	writeCreds(t, profileCreds, "stale", "stale_r", -1)

	isolateCreds := filepath.Join(p.IsolatesRoot, "work", ".credentials.json")
	writeCreds(t, isolateCreds, "isolate_fresh", "isolate_fresh_r", 2)

	rep, err := Reconcile(s, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.TokensPromoted) != 1 || rep.TokensPromoted[0] != "work" {
		t.Errorf("TokensPromoted = %v, want [work]", rep.TokensPromoted)
	}
	data, _ := os.ReadFile(profileCreds)
	if !contains(string(data), "isolate_fresh") {
		t.Errorf("isolate tokens not promoted: %s", data)
	}
}

// Orphan isolate cleanup: Location=="isolated" but no claude process owns
// the isolate dir → reconcile downgrades to dormant.
// (Runs only on Linux — macOS returns true conservatively.)
func TestReconcile_OrphanIsolate_Cleared(t *testing.T) {
	if _, err := os.Stat("/proc/self/exe"); err != nil {
		t.Skip("requires /proc (Linux)")
	}
	p := makeEnv(t)
	s := profile.NewStore()
	s.Profiles["work"] = sampleProfile("work", "a@x.com")
	s.Profiles["work"].Location = profile.LocationIsolated

	// Create an isolate dir without any running claude process.
	profileCreds := filepath.Join(p.ProfilesRoot, "work", "credentials.json")
	writeCreds(t, profileCreds, "tok", "ref", 1)
	isolateCreds := filepath.Join(p.IsolatesRoot, "work", ".credentials.json")
	writeCreds(t, isolateCreds, "tok", "ref", 1)

	rep, err := Reconcile(s, p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Profiles["work"].Location != profile.LocationDormant {
		t.Errorf("orphan not cleared: Location = %q", s.Profiles["work"].Location)
	}
	if len(rep.OrphansCleared) != 1 {
		t.Errorf("OrphansCleared = %v, want 1 entry", rep.OrphansCleared)
	}
}

// Active pointer correction: live identity matches a profile that's not
// marked live in the store → reconcile corrects.
func TestReconcile_CorrectsActivePointerDrift(t *testing.T) {
	p := makeEnv(t)
	s := profile.NewStore()
	s.Profiles["work"] = sampleProfile("work", "work@x.com")
	s.Profiles["home"] = sampleProfile("home", "home@x.com")
	// Store thinks "work" is live...
	_ = s.SetActive("work")

	// ...but live is actually "home" (user ran claude /logout+/login externally).
	writeClaudeJSON(t, p.ClaudeJSONPath, "home@x.com", "org-home")
	liveCreds := filepath.Join(p.ClaudeConfigHome, ".credentials.json")
	writeCreds(t, liveCreds, "home_tok", "home_r", 1)

	profileWorkCreds := filepath.Join(p.ProfilesRoot, "work", "credentials.json")
	writeCreds(t, profileWorkCreds, "work_tok", "work_r", -1)
	profileHomeCreds := filepath.Join(p.ProfilesRoot, "home", "credentials.json")
	writeCreds(t, profileHomeCreds, "old_home", "old_home_r", -1)

	rep, err := Reconcile(s, p)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.ActiveCorrected {
		t.Errorf("ActiveCorrected should be true")
	}
	if s.Active == nil || *s.Active != "home" {
		t.Errorf("Active = %v, want home", s.Active)
	}
	if s.Profiles["home"].Location != profile.LocationLive {
		t.Errorf("home.Location = %q, want live", s.Profiles["home"].Location)
	}
	if s.Profiles["work"].Location != profile.LocationDormant {
		t.Errorf("work.Location = %q, want dormant", s.Profiles["work"].Location)
	}
}

// Defensive: duplicate identity detection reports the corruption without
// erroring (so claudeorch can still run), but surfaces it in the report.
func TestReconcile_DuplicateIdentity_Reported(t *testing.T) {
	p := makeEnv(t)
	s := profile.NewStore()
	s.Profiles["a"] = sampleProfile("a", "same@x.com")
	s.Profiles["a"].OrganizationUUID = "same-org"
	s.Profiles["b"] = sampleProfile("b", "same@x.com")
	s.Profiles["b"].OrganizationUUID = "same-org"

	rep, err := Reconcile(s, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.DuplicateIdentities) == 0 {
		t.Error("expected duplicate identities reported")
	}
}

// MarkIsolated is idempotent: calling it on an already-isolated profile
// should be a no-op, not an error.
func TestMarkIsolated_Idempotent(t *testing.T) {
	s := profile.NewStore()
	s.Profiles["work"] = sampleProfile("work", "a@x.com")

	if err := s.MarkIsolated("work"); err != nil {
		t.Fatal(err)
	}
	// Second call should succeed silently.
	if err := s.MarkIsolated("work"); err != nil {
		t.Errorf("second MarkIsolated errored: %v", err)
	}
	if s.Profiles["work"].Location != profile.LocationIsolated {
		t.Errorf("Location = %q, want isolated", s.Profiles["work"].Location)
	}
}

// The dangerous double-holder state: profile is isolated (launched session
// still owns the isolate dir) AND live ~/.claude/ identity matches the same
// profile. Reconcile must NOT auto-promote to "live" — that would create
// the exact OAuth refresh-token-rotation race the whole design prevents.
func TestReconcile_IsolatedLiveConflict_Surfaced(t *testing.T) {
	if _, err := os.Stat("/proc/self/exe"); err != nil {
		t.Skip("requires /proc to simulate a live isolate owner")
	}
	p := makeEnv(t)
	s := profile.NewStore()
	s.Profiles["work"] = sampleProfile("work", "a@x.com")
	s.Profiles["work"].Location = profile.LocationIsolated

	// Live matches the same identity — the conflict scenario.
	writeClaudeJSON(t, p.ClaudeJSONPath, "a@x.com", "org-work")
	liveCreds := filepath.Join(p.ClaudeConfigHome, ".credentials.json")
	writeCreds(t, liveCreds, "live_tok", "live_r", 1)

	profileCreds := filepath.Join(p.ProfilesRoot, "work", "credentials.json")
	writeCreds(t, profileCreds, "profile_tok", "profile_r", 2)

	// Materialize an isolate dir so the orphan check thinks something owns it.
	// We lie by creating the dir and pointing a background `sleep` at it via
	// CLAUDE_CONFIG_DIR to simulate a live launched claude session.
	isolateDir := filepath.Join(p.IsolatesRoot, "work")
	if err := os.MkdirAll(isolateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeCreds(t, filepath.Join(isolateDir, ".credentials.json"), "iso_tok", "iso_r", 1)

	cmd := exec.Command("sleep", "5")
	cmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+isolateDir)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()
	time.Sleep(20 * time.Millisecond) // let /proc/<pid>/environ populate

	rep, err := Reconcile(s, p)
	if err != nil {
		t.Fatal(err)
	}

	// The conflict must be surfaced.
	if len(rep.IsolatedLiveConflicts) != 1 || rep.IsolatedLiveConflicts[0] != "work" {
		t.Errorf("IsolatedLiveConflicts = %v, want [work]", rep.IsolatedLiveConflicts)
	}
	// Profile must NOT have been silently promoted to live.
	if s.Profiles["work"].Location != profile.LocationIsolated {
		t.Errorf("Location = %q, want isolated (no silent upgrade)", s.Profiles["work"].Location)
	}
}

// Unreadable / corrupted credentials: skipped silently, don't promote.
func TestReconcile_CorruptedCreds_SkippedSilently(t *testing.T) {
	p := makeEnv(t)
	s := profile.NewStore()
	s.Profiles["work"] = sampleProfile("work", "a@x.com")

	profileCreds := filepath.Join(p.ProfilesRoot, "work", "credentials.json")
	if err := os.MkdirAll(filepath.Dir(profileCreds), 0o700); err != nil {
		t.Fatal(err)
	}
	// Corrupted content.
	if err := os.WriteFile(profileCreds, []byte(`{not json`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Reconcile(s, p)
	if err != nil {
		t.Errorf("unexpected error on corrupted creds: %v", err)
	}
}

// contains is a small substring check to avoid importing strings for one call.
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
