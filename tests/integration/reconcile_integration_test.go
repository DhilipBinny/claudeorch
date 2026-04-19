//go:build integration

package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeCredsAt writes a synthetic credentials.json with the given expiry
// offset (in hours from now). Negative values = expired.
func writeCredsAt(t *testing.T, path, accessToken, refreshToken string, hoursFromNow int) {
	t.Helper()
	expiresMs := time.Now().Add(time.Duration(hoursFromNow)*time.Hour).UnixMilli()
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

// readCredsTokens returns the access+refresh tokens from a credentials.json.
func readCredsTokens(t *testing.T, path string) (access, refresh string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read creds %s: %v", path, err)
	}
	var d struct {
		ClaudeAiOauth struct {
			AccessToken  string `json:"accessToken"`
			RefreshToken string `json:"refreshToken"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatalf("parse creds %s: %v", path, err)
	}
	return d.ClaudeAiOauth.AccessToken, d.ClaudeAiOauth.RefreshToken
}

// TestSync_Empty reports "Already in sync" on an empty store.
func TestSync_Empty(t *testing.T) {
	env := NewEnv(t)
	r := env.Run("sync")
	r.AssertSuccess(t)
	r.AssertOutputContains(t, "Already in sync")
}

// TestSync_PromotesFresherLiveToProfile: the core fix. Plain claude has
// rotated tokens in ~/.claude/; sync pulls them back into the profile.
func TestSync_PromotesFresherLiveToProfile(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-uuid-1", "Acme")
	env.WriteCredentials("initial_access", "initial_refresh")
	env.Run("add", "work").AssertSuccess(t)

	// Simulate plain claude rotating the live tokens to something newer.
	liveCreds := filepath.Join(env.ClaudeConfigDir, ".credentials.json")
	writeCredsAt(t, liveCreds, "rotated_access", "rotated_refresh", 2)

	// Also age the profile copy so live is clearly fresher.
	profileCreds := filepath.Join(env.ProfileDir("work"), "credentials.json")
	writeCredsAt(t, profileCreds, "initial_access", "initial_refresh", -1)

	r := env.Run("sync")
	r.AssertSuccess(t)
	r.AssertOutputContains(t, "refreshed profile/work")

	access, refresh := readCredsTokens(t, profileCreds)
	if access != "rotated_access" {
		t.Errorf("profile access token = %q, want rotated_access", access)
	}
	if refresh != "rotated_refresh" {
		t.Errorf("profile refresh token = %q, want rotated_refresh", refresh)
	}
}

// TestSwap_ReconcilesBeforeSwapping: the outgoing live profile's fresher
// tokens get saved back to its profile copy before we overwrite live with
// the target profile.
func TestSwap_ReconcilesBeforeSwapping(t *testing.T) {
	env := NewEnv(t)
	// Add 'work' first, making it active/live.
	env.WriteClaudeJSON("alice@example.com", "org-1", "Acme")
	env.WriteCredentials("work_initial_a", "work_initial_r")
	env.Run("add", "work").AssertSuccess(t)

	// Add 'home' (now active/live).
	env.WriteClaudeJSON("bob@example.com", "org-2", "BobCo")
	env.WriteCredentials("home_initial_a", "home_initial_r")
	env.Run("add", "home").AssertSuccess(t)

	// Simulate claude rotating home's tokens in ~/.claude/ while we've been
	// using it — the profile copy is now stale, live is fresh.
	liveCreds := filepath.Join(env.ClaudeConfigDir, ".credentials.json")
	writeCredsAt(t, liveCreds, "home_rotated_a", "home_rotated_r", 2)

	// Swap to work. Reconcile should notice home's live tokens are fresher
	// than home's profile copy, and sync them before swap overwrites live.
	env.Run("swap", "work").AssertSuccess(t)

	// home's profile should now hold the rotated tokens, not the stale ones.
	homeProfileCreds := filepath.Join(env.ProfileDir("home"), "credentials.json")
	access, _ := readCredsTokens(t, homeProfileCreds)
	if access != "home_rotated_a" {
		t.Errorf("home profile lost rotation: access token = %q, want home_rotated_a",
			access)
	}
}

// TestLaunch_DoesNotOverwriteFresherIsolateTokens: the exact v0.1.0 bug.
// A prior launch session rotated tokens in isolate/<name>/; a new launch
// of the same profile must not clobber them with stale profile copies.
func TestLaunch_DoesNotOverwriteFresherIsolateTokens(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-1", "Acme")
	env.WriteCredentials("stale_in_profile", "stale_r")
	env.Run("add", "work").AssertSuccess(t)

	// Age the profile copy to simulate passage of time.
	profileCreds := filepath.Join(env.ProfileDir("work"), "credentials.json")
	writeCredsAt(t, profileCreds, "stale_in_profile", "stale_r", -1)

	// Pre-populate the isolate with fresher tokens, as a prior launched
	// claude session would have done.
	isolateCreds := filepath.Join(env.ClaudeorchHome, "isolate", "work", ".credentials.json")
	writeCredsAt(t, isolateCreds, "isolate_fresh", "isolate_fresh_r", 2)

	// Run sync (any claudeorch command would do — sync is the simplest).
	// Sync's reconcile should promote isolate → profile.
	env.Run("sync").AssertSuccess(t)

	access, _ := readCredsTokens(t, profileCreds)
	if access != "isolate_fresh" {
		t.Errorf("isolate tokens not promoted; profile access = %q, want isolate_fresh",
			access)
	}
}

// TestLaunch_RefusesWhenProfileIsLive: can't launch a profile that's already
// live in ~/.claude/ — would fork OAuth refresh-token ownership.
func TestLaunch_RefusesWhenProfileIsLive(t *testing.T) {
	env := NewEnv(t)
	env.WriteClaudeJSON("alice@example.com", "org-1", "Acme")
	env.WriteCredentials("tok", "ref")
	env.Run("add", "work").AssertSuccess(t)
	// add marks 'work' active/live.

	r := env.Run("launch", "work")
	r.AssertError(t)
	if !strings.Contains(r.Stderr, "live in ~/.claude/") {
		t.Errorf("expected live-in-claude refuse, got stderr:\n%s", r.Stderr)
	}
}

// TestStoreV1_MigratesTransparently verifies end-to-end that a store.json
// written by v0.1.x is readable by v0.2.0+ via transparent migration.
func TestStoreV1_MigratesTransparently(t *testing.T) {
	env := NewEnv(t)

	// Plant a v1 store.json directly (as if it were written by v0.1.x).
	storePath := env.StoreFile()
	if err := os.MkdirAll(filepath.Dir(storePath), 0o700); err != nil {
		t.Fatal(err)
	}
	v1 := `{
		"version": 1,
		"active": "work",
		"profiles": {
			"work": {
				"email": "a@x.com", "organization_uuid": "org-1",
				"organization_name": "Acme",
				"created_at": "2026-04-17T15:00:00Z",
				"source": "oauth"
			}
		}
	}`
	if err := os.WriteFile(storePath, []byte(v1), 0o600); err != nil {
		t.Fatal(err)
	}
	// Also create the profile directory + a valid credentials file, so
	// downstream commands don't trip on missing files.
	profileDir := env.ProfileDir("work")
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeCredsAt(t, filepath.Join(profileDir, "credentials.json"), "tok", "ref", 1)
	env.WriteClaudeJSON("a@x.com", "org-1", "Acme")
	writeCredsAt(t, filepath.Join(env.ClaudeConfigDir, ".credentials.json"), "tok", "ref", 1)

	// Claude.json for the profile snapshot.
	if err := os.WriteFile(filepath.Join(profileDir, "claude.json"),
		[]byte(`{"oauthAccount":{"emailAddress":"a@x.com","organizationUuid":"org-1","organizationName":"Acme"}}`),
		0o600); err != nil {
		t.Fatal(err)
	}

	// status should work against the migrated store.
	r := env.Run("status", "--no-usage")
	r.AssertSuccess(t)
	r.AssertOutputContains(t, "work")

	// The save triggered by any later mutation should write v2 shape.
	data, _ := os.ReadFile(storePath)
	if strings.Contains(string(data), `"version": 1`) && !strings.Contains(string(data), `"version": 2`) {
		// status doesn't mutate, so store.json may still be v1 — that's fine
		// (we haven't saved yet). Run a mutating command to trigger save.
		env.Run("sync").AssertSuccess(t)
		data, _ = os.ReadFile(storePath)
	}
	if !strings.Contains(string(data), `"version": 2`) {
		t.Errorf("store.json not upgraded to v2 after mutation:\n%s", data)
	}
	if !strings.Contains(string(data), `"location":`) {
		t.Errorf("store.json missing location field post-migration:\n%s", data)
	}
}
