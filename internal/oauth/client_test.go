package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// withEndpoint swaps the package-level endpoint for the duration of a test.
func withEndpoint(t *testing.T, url string) {
	t.Helper()
	orig := tokenEndpoint
	tokenEndpoint = url
	t.Cleanup(func() { tokenEndpoint = orig })
}

// fakeCreds builds a minimal credentials blob for testing.
func fakeCreds(accessToken, refreshToken, customField string) []byte {
	m := map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken":  accessToken,
			"refreshToken": refreshToken,
			"expiresAt":    "2020-01-01T00:00:00Z",
			"scopes":       []string{"openai"},
		},
	}
	if customField != "" {
		m["customField"] = customField
	}
	data, _ := json.Marshal(m)
	return data
}

func TestMergeCredentials_PreservesUnknownFields(t *testing.T) {
	orig := fakeCreds("old_access", "old_refresh", "should_survive")
	result, err := mergeCredentials(orig, "new_access", "new_refresh",
		time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("mergeCredentials: %v", err)
	}
	if !strings.Contains(string(result), "new_access") {
		t.Errorf("new accessToken not in merged blob: %s", result)
	}
	if !strings.Contains(string(result), "should_survive") {
		t.Errorf("custom field lost in merge: %s", result)
	}
	if !strings.Contains(string(result), "openai") {
		t.Errorf("scopes lost in merge: %s", result)
	}
}

func TestMergeCredentials_NumericExpiresAt_StaysNumeric(t *testing.T) {
	// Real Claude Code writes expiresAt as an integer (ms epoch).
	// A refresh must NOT rewrite it as a string — doing so could confuse
	// Claude Code's own parser.
	orig := []byte(`{"claudeAiOauth":{"accessToken":"old","refreshToken":"old_r","expiresAt":1776459847964,"scopes":["x"]}}`)
	result, err := mergeCredentials(orig, "new_access", "new_refresh",
		time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	// Must not contain a string-shaped expiresAt.
	if strings.Contains(string(result), `"expiresAt":"`) {
		t.Errorf("numeric expiresAt was wrongly rewritten as string: %s", result)
	}
	// Must contain numeric ms-epoch.
	var out map[string]any
	_ = json.Unmarshal(result, &out)
	exp := out["claudeAiOauth"].(map[string]any)["expiresAt"]
	if _, ok := exp.(float64); !ok {
		t.Errorf("expiresAt should be numeric, got %T: %v", exp, exp)
	}
}

func TestMergeCredentials_StringExpiresAt_StaysString(t *testing.T) {
	orig := []byte(`{"claudeAiOauth":{"accessToken":"old","refreshToken":"old_r","expiresAt":"2020-01-01T00:00:00Z"}}`)
	result, err := mergeCredentials(orig, "new_access", "new_refresh",
		time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result), `"expiresAt":"2030-01-01T00:00:00Z"`) {
		t.Errorf("string expiresAt was wrongly rewritten or dropped: %s", result)
	}
}

func TestMergeCredentials_EmptyRefreshToken_KeepsOld(t *testing.T) {
	orig := fakeCreds("old_access", "old_refresh", "")
	result, err := mergeCredentials(orig, "new_access", "",
		time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result), "old_refresh") {
		t.Errorf("old refreshToken not preserved: %s", result)
	}
}

func TestRefresh_MissingRefreshToken(t *testing.T) {
	blob := []byte(`{"claudeAiOauth":{}}`)
	_, err := Refresh(context.Background(), blob)
	if !errors.Is(err, ErrSchema) {
		t.Errorf("expected ErrSchema, got: %v", err)
	}
}

func TestRefresh_MalformedBlob(t *testing.T) {
	_, err := Refresh(context.Background(), []byte("not json"))
	if !errors.Is(err, ErrSchema) {
		t.Errorf("expected ErrSchema, got: %v", err)
	}
}

func TestRefresh_Happy_RotatesTokens_PreservesUnknownFields(t *testing.T) {
	var gotBeta, gotCT string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBeta = r.Header.Get("anthropic-beta")
		gotCT = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "new_access_xyz",
			"refresh_token": "new_refresh_abc",
			"expires_in":    3600,
		})
	}))
	defer srv.Close()
	withEndpoint(t, srv.URL)

	orig := fakeCreds("old_access", "old_refresh", "custom_payload")
	result, err := Refresh(context.Background(), orig)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(result, &out); err != nil {
		t.Fatalf("parse result: %v", err)
	}
	oauth := out["claudeAiOauth"].(map[string]any)
	if oauth["accessToken"] != "new_access_xyz" {
		t.Errorf("accessToken not rotated: %v", oauth["accessToken"])
	}
	if oauth["refreshToken"] != "new_refresh_abc" {
		t.Errorf("refreshToken not rotated: %v", oauth["refreshToken"])
	}
	// Unknown field must survive through refresh.
	if out["customField"] != "custom_payload" {
		t.Errorf("customField lost: %v", out["customField"])
	}
	// Scopes are inside claudeAiOauth — also must survive.
	if _, ok := oauth["scopes"]; !ok {
		t.Error("scopes field dropped through refresh")
	}
	// Wire-level checks.
	if gotBeta != oauthBetaHdr {
		t.Errorf("anthropic-beta header = %q, want %q", gotBeta, oauthBetaHdr)
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotCT)
	}
	if gotBody["grant_type"] != "refresh_token" {
		t.Errorf("grant_type = %v, want refresh_token", gotBody["grant_type"])
	}
	if gotBody["refresh_token"] != "old_refresh" {
		t.Errorf("refresh_token sent = %v, want old_refresh", gotBody["refresh_token"])
	}
	if gotBody["client_id"] != clientID {
		t.Errorf("client_id = %v, want %s", gotBody["client_id"], clientID)
	}
}

func TestRefresh_InvalidGrant(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()
	withEndpoint(t, srv.URL)

	_, err := Refresh(context.Background(), fakeCreds("a", "b", ""))
	if !errors.Is(err, ErrInvalidGrant) {
		t.Errorf("expected ErrInvalidGrant, got: %v", err)
	}
}

func TestRefresh_5xx_ReturnsErrNetwork(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"error":"internal"}`))
	}))
	defer srv.Close()
	withEndpoint(t, srv.URL)

	_, err := Refresh(context.Background(), fakeCreds("a", "b", ""))
	if !errors.Is(err, ErrNetwork) {
		t.Errorf("expected ErrNetwork, got: %v", err)
	}
	if errors.Is(err, ErrInvalidGrant) {
		t.Error("500 wrongly classified as invalid_grant")
	}
}

func TestRefresh_MissingAccessToken_IsSchemaError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"refresh_token": "new_ref",
			"expires_in":    3600,
			// access_token missing
		})
	}))
	defer srv.Close()
	withEndpoint(t, srv.URL)

	_, err := Refresh(context.Background(), fakeCreds("a", "b", ""))
	if !errors.Is(err, ErrSchema) {
		t.Errorf("expected ErrSchema when access_token missing, got: %v", err)
	}
}

func TestRefresh_EmptyRefreshInResponse_KeepsOld(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "new_access_only",
			"expires_in":   3600,
			// refresh_token omitted — server may rotate only access token
		})
	}))
	defer srv.Close()
	withEndpoint(t, srv.URL)

	orig := fakeCreds("old_access", "old_refresh", "")
	result, err := Refresh(context.Background(), orig)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if !strings.Contains(string(result), "old_refresh") {
		t.Errorf("old refresh_token should survive when server omits it: %s", result)
	}
	if !strings.Contains(string(result), "new_access_only") {
		t.Errorf("new access_token missing from result: %s", result)
	}
}
