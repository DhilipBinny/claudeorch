package usage

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// withEndpoint swaps the package-level endpoint for the duration of a test.
func withEndpoint(t *testing.T, url string) {
	t.Helper()
	orig := usageEndpoint
	usageEndpoint = url
	t.Cleanup(func() { usageEndpoint = orig })
}

func TestFetch_Happy(t *testing.T) {
	reset := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	var gotAuth, gotBeta string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotBeta = r.Header.Get("anthropic-beta")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"input_tokens": 500000, "output_tokens": 200000},
				{"input_tokens": 100000, "output_tokens": 50000},
			},
			"limit":    2000000,
			"reset_at": reset.Format(time.RFC3339),
		})
	}))
	defer srv.Close()
	withEndpoint(t, srv.URL)

	u, err := Fetch(context.Background(), "test-token")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if u.UsedTokens != 850000 {
		t.Errorf("UsedTokens = %d, want 850000", u.UsedTokens)
	}
	if u.LimitTokens != 2000000 {
		t.Errorf("LimitTokens = %d, want 2000000", u.LimitTokens)
	}
	if !u.ResetAt.Equal(reset) {
		t.Errorf("ResetAt = %v, want %v", u.ResetAt, reset)
	}
	if gotAuth != "Bearer test-token" {
		t.Errorf("Authorization header = %q, want Bearer test-token", gotAuth)
	}
	if gotBeta != oauthBetaHdr {
		t.Errorf("anthropic-beta header = %q, want %q", gotBeta, oauthBetaHdr)
	}
}

func TestFetch_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer srv.Close()
	withEndpoint(t, srv.URL)

	_, err := Fetch(context.Background(), "bad-token")
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got: %v", err)
	}
}

func TestFetch_5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	withEndpoint(t, srv.URL)

	_, err := Fetch(context.Background(), "tok")
	if err == nil {
		t.Fatal("expected error on 500, got nil")
	}
	if errors.Is(err, ErrUnauthorized) {
		t.Error("500 should not be reported as unauthorized")
	}
}

func TestFetch_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()
	withEndpoint(t, srv.URL)

	_, err := Fetch(context.Background(), "tok")
	if err == nil {
		t.Fatal("expected error on malformed JSON, got nil")
	}
}

func TestFetch_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	withEndpoint(t, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := Fetch(ctx, "tok")
	if err == nil {
		t.Fatal("expected error on cancelled context, got nil")
	}
}

func TestPercentUsed(t *testing.T) {
	cases := []struct {
		used, limit int64
		want        float64
	}{
		{0, 1000, 0},
		{500, 1000, 0.5},
		{1000, 1000, 1.0},
		{1200, 1000, 1.0}, // clamped
		{100, 0, 0},       // unknown limit
	}
	for _, tc := range cases {
		u := &Usage{UsedTokens: tc.used, LimitTokens: tc.limit}
		got := u.PercentUsed()
		if got != tc.want {
			t.Errorf("PercentUsed(%d/%d) = %.3f, want %.3f", tc.used, tc.limit, got, tc.want)
		}
	}
}
