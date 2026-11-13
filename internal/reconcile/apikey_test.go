package reconcile

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DhilipBinny/claudeorch/internal/profile"
)

const apiKeyBlob = `{"apiKey":"sk-ant-test123"}`

func apiKeyProfile(name string) *profile.Profile {
	p := sampleProfile(name, name+"@x.com")
	p.Source = profile.SourceAPIKey
	return p
}

func writeRaw(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// API keys do not rotate, so the OAuth "freshest tokens win" sync must not
// touch an API-key profile, even when an isolate holds a fresher OAuth blob.
func TestReconcile_APIKeyProfile_SkipsFreshnessSync(t *testing.T) {
	p := makeEnv(t)
	s := profile.NewStore()
	s.Profiles["keys"] = apiKeyProfile("keys")

	profileCreds := filepath.Join(p.ProfilesRoot, "keys", "credentials.json")
	writeRaw(t, profileCreds, apiKeyBlob)
	writeCreds(t, filepath.Join(p.IsolatesRoot, "keys", ".credentials.json"), "fresher_tok", "fresher_ref", 5)

	if _, err := Reconcile(s, p); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(profileCreds)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != apiKeyBlob {
		t.Errorf("API-key profile credentials were overwritten: %s", got)
	}
}

// The freshness skip must not also skip orphan cleanup: a dead isolate
// session on an API-key profile is still downgraded to dormant.
func TestReconcile_APIKeyProfile_OrphanIsolateStillCleared(t *testing.T) {
	if _, err := os.Stat("/proc/self/exe"); err != nil {
		t.Skip("requires /proc (Linux)")
	}
	p := makeEnv(t)
	s := profile.NewStore()
	s.Profiles["keys"] = apiKeyProfile("keys")
	s.Profiles["keys"].Location = profile.LocationIsolated

	writeRaw(t, filepath.Join(p.ProfilesRoot, "keys", "credentials.json"), apiKeyBlob)
	writeRaw(t, filepath.Join(p.IsolatesRoot, "keys", ".credentials.json"), apiKeyBlob)

	rep, err := Reconcile(s, p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Profiles["keys"].Location != profile.LocationDormant {
		t.Errorf("orphan not cleared: Location = %q", s.Profiles["keys"].Location)
	}
	if len(rep.OrphansCleared) != 1 {
		t.Errorf("OrphansCleared = %v, want 1 entry", rep.OrphansCleared)
	}
}
