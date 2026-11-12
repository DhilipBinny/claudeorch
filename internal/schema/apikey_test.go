package schema

import (
	"strings"
	"testing"
)

func TestParseCredentials_APIKey(t *testing.T) {
	c, err := ParseCredentials([]byte(`{"apiKey":"sk-ant-test123"}`))
	if err != nil {
		t.Fatalf("ParseCredentials: %v", err)
	}
	if c.Type != CredentialAPIKey {
		t.Errorf("Type = %v, want CredentialAPIKey", c.Type)
	}
	if c.APIKey != "sk-ant-test123" {
		t.Errorf("APIKey = %q, want sk-ant-test123", c.APIKey)
	}
	if !c.ExpiresAt.IsZero() || c.AccessToken != "" || c.RefreshToken != "" {
		t.Errorf("API-key credentials must carry no OAuth fields: %+v", c)
	}
}

func TestParseCredentials_APIKey_TrimsWhitespace(t *testing.T) {
	c, err := ParseCredentials([]byte(`{"apiKey":"  sk-ant-test123\n"}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.APIKey != "sk-ant-test123" {
		t.Errorf("APIKey = %q, want trimmed", c.APIKey)
	}
}

// A blob holding both formats resolves to the API key. This is the
// documented precedence (see ParseCredentials), not an accident.
func TestParseCredentials_BothFormats_APIKeyWins(t *testing.T) {
	blob := `{"apiKey":"sk-ant-test123","claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":"2030-01-01T00:00:00Z"}}`
	c, err := ParseCredentials([]byte(blob))
	if err != nil {
		t.Fatal(err)
	}
	if c.Type != CredentialAPIKey || c.APIKey != "sk-ant-test123" {
		t.Errorf("got Type=%v APIKey=%q, want the API key to win", c.Type, c.APIKey)
	}
}

// A blank apiKey is "not present", so valid OAuth data underneath is used.
func TestParseCredentials_BlankAPIKey_FallsBackToOAuth(t *testing.T) {
	blob := `{"apiKey":"   ","claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":"2030-01-01T00:00:00Z"}}`
	c, err := ParseCredentials([]byte(blob))
	if err != nil {
		t.Fatal(err)
	}
	if c.Type != CredentialOAuth || c.AccessToken != "a" {
		t.Errorf("got Type=%v AccessToken=%q, want OAuth fallback", c.Type, c.AccessToken)
	}
}

func TestParseCredentials_BlankAPIKeyOnly_IsAnError(t *testing.T) {
	if _, err := ParseCredentials([]byte(`{"apiKey":""}`)); err == nil {
		t.Fatal("expected an error: neither a key nor OAuth data is present")
	}
}

func TestMakeAPIKeyCredentialBlob_RoundTrips(t *testing.T) {
	blob, err := MakeAPIKeyCredentialBlob(" sk-ant-test123 ")
	if err != nil {
		t.Fatalf("MakeAPIKeyCredentialBlob: %v", err)
	}
	c, err := ParseCredentials(blob)
	if err != nil {
		t.Fatalf("blob does not parse back: %v (blob %s)", err, blob)
	}
	if c.Type != CredentialAPIKey || c.APIKey != "sk-ant-test123" {
		t.Errorf("round trip gave Type=%v APIKey=%q", c.Type, c.APIKey)
	}
}

func TestMakeAPIKeyCredentialBlob_RejectsEmptyKey(t *testing.T) {
	for _, key := range []string{"", "   ", "\n\t"} {
		blob, err := MakeAPIKeyCredentialBlob(key)
		if err == nil {
			t.Errorf("key %q: expected an error, got blob %s", key, blob)
			continue
		}
		if !strings.Contains(err.Error(), "empty") {
			t.Errorf("key %q: error %q should say the key is empty", key, err)
		}
	}
}
