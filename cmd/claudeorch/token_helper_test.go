package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/DhilipBinny/claudeorch/internal/profile"
)

// The usage endpoint only accepts OAuth access tokens. For an API-key profile
// freshAccessToken must NOT return the key (callers send it as a bearer
// token); it reports that no usage data exists instead.
func TestFreshAccessToken_APIKeyProfile_NeverReturnsTheKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDEORCH_HOME", home)

	dir := filepath.Join(home, "profiles", "keys")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "credentials.json"),
		[]byte(`{"apiKey":"sk-ant-test123"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	store := profile.NewStore()
	store.Profiles["keys"] = &profile.Profile{Name: "keys", Source: profile.SourceAPIKey}

	token, refreshed, err := freshAccessToken("keys", store, filepath.Join(home, "store.json"))
	if token != "" {
		t.Errorf("token = %q: the API key must never be returned as an access token", token)
	}
	if refreshed {
		t.Error("refreshed = true, want false")
	}
	if !errors.Is(err, errNoUsageForAPIKey) {
		t.Errorf("err = %v, want errNoUsageForAPIKey", err)
	}

	// And the usage fetch built on it must fail before any network call.
	if _, ferr := fetchUsageWithToken(token, err); !errors.Is(ferr, errNoUsageForAPIKey) {
		t.Errorf("fetchUsageWithToken err = %v, want errNoUsageForAPIKey", ferr)
	}
}
