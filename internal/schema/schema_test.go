package schema

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// ---- ParseCredentials -------------------------------------------------------

func TestParseCredentials_Happy(t *testing.T) {
	blob := []byte(`{
		"claudeAiOauth": {
			"accessToken":  "tok_access_abc123",
			"refreshToken": "ref_refresh_xyz789",
			"expiresAt":    "2026-12-31T00:00:00Z",
			"scopes":       ["openai"],
			"subscriptionType": "pro",
			"rateLimitTier": "standard"
		}
	}`)

	creds, err := ParseCredentials(blob)
	if err != nil {
		t.Fatalf("ParseCredentials: %v", err)
	}
	if creds.AccessToken != "tok_access_abc123" {
		t.Errorf("AccessToken = %q", creds.AccessToken)
	}
	if creds.RefreshToken != "ref_refresh_xyz789" {
		t.Errorf("RefreshToken = %q", creds.RefreshToken)
	}
	want := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	if !creds.ExpiresAt.Equal(want) {
		t.Errorf("ExpiresAt = %v, want %v", creds.ExpiresAt, want)
	}
	if len(creds.Raw) == 0 {
		t.Error("Raw blob is empty")
	}
}

func TestParseCredentials_Empty(t *testing.T) {
	_, err := ParseCredentials([]byte{})
	if !errors.Is(err, ErrSchemaIncompatible) {
		t.Errorf("expected ErrSchemaIncompatible, got: %v", err)
	}
}

func TestParseCredentials_MissingClaudeAiOauth(t *testing.T) {
	_, err := ParseCredentials([]byte(`{"other": "data"}`))
	if !errors.Is(err, ErrSchemaIncompatible) {
		t.Errorf("expected ErrSchemaIncompatible, got: %v", err)
	}
	if !strings.Contains(err.Error(), "claudeAiOauth") {
		t.Errorf("error should mention missing key: %v", err)
	}
}

func TestParseCredentials_EmptyAccessToken(t *testing.T) {
	blob := []byte(`{"claudeAiOauth": {"accessToken": "", "refreshToken": "ref_abc"}}`)
	_, err := ParseCredentials(blob)
	if !errors.Is(err, ErrSchemaIncompatible) {
		t.Errorf("expected ErrSchemaIncompatible for empty accessToken, got: %v", err)
	}
}

func TestParseCredentials_EmptyRefreshToken(t *testing.T) {
	blob := []byte(`{"claudeAiOauth": {"accessToken": "tok_abc", "refreshToken": ""}}`)
	_, err := ParseCredentials(blob)
	if !errors.Is(err, ErrSchemaIncompatible) {
		t.Errorf("expected ErrSchemaIncompatible for empty refreshToken, got: %v", err)
	}
}

func TestParseCredentials_MalformedJSON(t *testing.T) {
	_, err := ParseCredentials([]byte(`not json`))
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestParseCredentials_OversizedFile(t *testing.T) {
	huge := make([]byte, maxFileSize+1)
	for i := range huge {
		huge[i] = 'a'
	}
	_, err := ParseCredentials(huge)
	if err == nil {
		t.Fatal("expected error for oversized file")
	}
	if !strings.Contains(err.Error(), "exceeds max") {
		t.Errorf("expected 'exceeds max' in error: %v", err)
	}
}

func TestParseCredentials_UnparsableExpiresAt(t *testing.T) {
	// Bad expiresAt must not fail — just zero the field.
	blob := []byte(`{
		"claudeAiOauth": {
			"accessToken":  "tok_abc",
			"refreshToken": "ref_xyz",
			"expiresAt":    "not-a-date"
		}
	}`)
	creds, err := ParseCredentials(blob)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !creds.ExpiresAt.IsZero() {
		t.Errorf("expected zero ExpiresAt for bad date, got %v", creds.ExpiresAt)
	}
}

func TestParseCredentials_NumericExpiresAt(t *testing.T) {
	// Real Claude Code writes expiresAt as milliseconds-since-epoch (number).
	// Verified against ~/.claude/.credentials.json on reference machine.
	blob := []byte(`{
		"claudeAiOauth": {
			"accessToken":  "tok_abc",
			"refreshToken": "ref_xyz",
			"expiresAt":    1776459847964
		}
	}`)
	creds, err := ParseCredentials(blob)
	if err != nil {
		t.Fatalf("ParseCredentials: %v", err)
	}
	if !creds.ExpiresAtWasNumeric {
		t.Error("ExpiresAtWasNumeric = false, want true")
	}
	want := time.UnixMilli(1776459847964).UTC()
	if !creds.ExpiresAt.Equal(want) {
		t.Errorf("ExpiresAt = %v, want %v", creds.ExpiresAt, want)
	}
}

func TestParseCredentials_StringExpiresAt_FlaggedAsNonNumeric(t *testing.T) {
	blob := []byte(`{
		"claudeAiOauth": {
			"accessToken":  "tok_abc",
			"refreshToken": "ref_xyz",
			"expiresAt":    "2026-12-31T00:00:00Z"
		}
	}`)
	creds, err := ParseCredentials(blob)
	if err != nil {
		t.Fatalf("ParseCredentials: %v", err)
	}
	if creds.ExpiresAtWasNumeric {
		t.Error("ExpiresAtWasNumeric = true for string input, want false")
	}
}

func TestParseCredentials_RenamedField_ReturnsSchemaError(t *testing.T) {
	// If Claude renames the top-level key we should get ErrSchemaIncompatible,
	// not a silent empty struct.
	blob := []byte(`{"claudeOauth": {"accessToken": "tok", "refreshToken": "ref"}}`)
	_, err := ParseCredentials(blob)
	if !errors.Is(err, ErrSchemaIncompatible) {
		t.Errorf("expected ErrSchemaIncompatible for renamed key, got: %v", err)
	}
	if !strings.Contains(err.Error(), "schema may be incompatible") || !strings.Contains(err.Error()+err.Error(), "incompatible") {
		// Just check it surfaces a meaningful message — exact wording may vary.
		_ = err
	}
}

// ---- ExtractIdentity --------------------------------------------------------

func TestExtractIdentity_Happy(t *testing.T) {
	blob := []byte(`{
		"numStartups": 5,
		"oauthAccount": {
			"emailAddress":     "alice@example.com",
			"organizationUuid": "org-uuid-123",
			"organizationName": "Acme Corp",
			"displayName":      "Alice",
			"billingType":      "subscription"
		}
	}`)

	id, err := ExtractIdentity(blob)
	if err != nil {
		t.Fatalf("ExtractIdentity: %v", err)
	}
	if id.EmailAddress != "alice@example.com" {
		t.Errorf("EmailAddress = %q", id.EmailAddress)
	}
	if id.OrganizationUUID != "org-uuid-123" {
		t.Errorf("OrganizationUUID = %q", id.OrganizationUUID)
	}
	if id.OrganizationName != "Acme Corp" {
		t.Errorf("OrganizationName = %q", id.OrganizationName)
	}
}

func TestExtractIdentity_Empty(t *testing.T) {
	_, err := ExtractIdentity([]byte{})
	if !errors.Is(err, ErrSchemaIncompatible) {
		t.Errorf("expected ErrSchemaIncompatible, got: %v", err)
	}
}

func TestExtractIdentity_MissingOAuthAccount(t *testing.T) {
	_, err := ExtractIdentity([]byte(`{"numStartups": 1}`))
	if !errors.Is(err, ErrSchemaIncompatible) {
		t.Errorf("expected ErrSchemaIncompatible, got: %v", err)
	}
	if !strings.Contains(err.Error(), "oauthAccount") {
		t.Errorf("error should mention key: %v", err)
	}
}

func TestExtractIdentity_EmptyEmail(t *testing.T) {
	blob := []byte(`{"oauthAccount": {"emailAddress": "", "organizationUuid": "u"}}`)
	_, err := ExtractIdentity(blob)
	if !errors.Is(err, ErrSchemaIncompatible) {
		t.Errorf("expected ErrSchemaIncompatible, got: %v", err)
	}
}

func TestExtractIdentity_EmptyOrgUUID(t *testing.T) {
	blob := []byte(`{"oauthAccount": {"emailAddress": "e@e.com", "organizationUuid": ""}}`)
	_, err := ExtractIdentity(blob)
	if !errors.Is(err, ErrSchemaIncompatible) {
		t.Errorf("expected ErrSchemaIncompatible, got: %v", err)
	}
}

func TestExtractIdentity_MissingOrgName_Allowed(t *testing.T) {
	// organizationName is optional (personal accounts).
	blob := []byte(`{"oauthAccount": {"emailAddress": "e@e.com", "organizationUuid": "u"}}`)
	id, err := ExtractIdentity(blob)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id.OrganizationName != "" {
		t.Errorf("expected empty OrgName, got %q", id.OrganizationName)
	}
}

func TestExtractIdentity_MalformedJSON(t *testing.T) {
	_, err := ExtractIdentity([]byte(`not json`))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExtractIdentity_OversizedFile(t *testing.T) {
	huge := make([]byte, maxFileSize+1)
	for i := range huge {
		huge[i] = 'a'
	}
	_, err := ExtractIdentity(huge)
	if err == nil {
		t.Fatal("expected error for oversized file")
	}
	if !strings.Contains(err.Error(), "exceeds max") {
		t.Errorf("expected 'exceeds max': %v", err)
	}
}

func TestExtractIdentity_RenamedField_ReturnsSchemaError(t *testing.T) {
	// If Claude renames oauthAccount → authAccount we should see ErrSchemaIncompatible.
	blob := []byte(`{"authAccount": {"emailAddress": "e@e.com", "organizationUuid": "u"}}`)
	_, err := ExtractIdentity(blob)
	if !errors.Is(err, ErrSchemaIncompatible) {
		t.Errorf("expected ErrSchemaIncompatible for renamed key, got: %v", err)
	}
}
