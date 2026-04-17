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

func TestFetch_Happy_RealShape(t *testing.T) {
	var gotAuth, gotBeta string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotBeta = r.Header.Get("anthropic-beta")
		w.Header().Set("Content-Type", "application/json")
		// Exact shape observed from the real endpoint.
		_, _ = w.Write([]byte(`{
			"five_hour":  {"utilization": 9.0, "resets_at": "2026-04-17T16:00:00.699600+00:00"},
			"seven_day":  {"utilization": 7.5, "resets_at": "2026-04-23T20:00:00.699617+00:00"},
			"seven_day_sonnet": {"utilization": 3.0, "resets_at": "2026-04-23T20:00:00.699625+00:00"},
			"extra_usage": {"is_enabled": true, "monthly_limit": 7000, "used_credits": 100.0, "utilization": 1.4, "currency": "USD"}
		}`))
	}))
	defer srv.Close()
	withEndpoint(t, srv.URL)

	u, err := Fetch(context.Background(), "test-token")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if u.FiveHour.Percent < 0.089 || u.FiveHour.Percent > 0.091 {
		t.Errorf("FiveHour.Percent = %f, want ~0.09", u.FiveHour.Percent)
	}
	if u.SevenDay.Percent < 0.074 || u.SevenDay.Percent > 0.076 {
		t.Errorf("SevenDay.Percent = %f, want ~0.075", u.SevenDay.Percent)
	}
	if u.FiveHour.ResetsAt.IsZero() {
		t.Error("FiveHour.ResetsAt should be parsed")
	}
	if u.SevenDay.ResetsAt.IsZero() {
		t.Error("SevenDay.ResetsAt should be parsed")
	}
	if gotAuth != "Bearer test-token" {
		t.Errorf("Authorization header = %q", gotAuth)
	}
	if gotBeta != oauthBetaHdr {
		t.Errorf("anthropic-beta header = %q", gotBeta)
	}
}

func TestFetch_ClampsUtilizationToZeroAndOne(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"five_hour": {"utilization": 150.0, "resets_at": null},
			"seven_day": {"utilization": -5.0,  "resets_at": null}
		}`))
	}))
	defer srv.Close()
	withEndpoint(t, srv.URL)

	u, err := Fetch(context.Background(), "tok")
	if err != nil {
		t.Fatal(err)
	}
	if u.FiveHour.Percent != 1.0 {
		t.Errorf("FiveHour overflow not clamped: %f", u.FiveHour.Percent)
	}
	if u.SevenDay.Percent != 0.0 {
		t.Errorf("SevenDay negative not clamped: %f", u.SevenDay.Percent)
	}
}

func TestFetch_MissingWindows_ReturnsZeroValues(t *testing.T) {
	// If server omits five_hour or seven_day entirely, don't crash.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	withEndpoint(t, srv.URL)

	u, err := Fetch(context.Background(), "tok")
	if err != nil {
		t.Fatal(err)
	}
	if u.FiveHour.Percent != 0 || !u.FiveHour.ResetsAt.IsZero() {
		t.Errorf("FiveHour should be zero-valued: %+v", u.FiveHour)
	}
	if u.SevenDay.Percent != 0 || !u.SevenDay.ResetsAt.IsZero() {
		t.Errorf("SevenDay should be zero-valued: %+v", u.SevenDay)
	}
}

func TestFetch_NullResetsAt_IsZeroTime(t *testing.T) {
	// Real API sometimes sends "resets_at": null.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"five_hour": {"utilization": 50.0, "resets_at": null},
			"seven_day": {"utilization": 50.0, "resets_at": null}
		}`))
	}))
	defer srv.Close()
	withEndpoint(t, srv.URL)

	u, err := Fetch(context.Background(), "tok")
	if err != nil {
		t.Fatal(err)
	}
	if !u.FiveHour.ResetsAt.IsZero() {
		t.Error("null resets_at should yield zero time")
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

// Verifies json.RawMessage-free parsing still handles both microsecond-precision
// and plain RFC3339 timestamps (both are in the wild).
func TestFetch_TimestampFormats(t *testing.T) {
	cases := []string{
		"2026-04-17T16:00:00.699600+00:00",
		"2026-04-17T16:00:00Z",
		"2026-04-17T16:00:00.1Z",
	}
	for _, ts := range cases {
		ts := ts
		t.Run(ts, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{
				"five_hour": map[string]any{"utilization": 50.0, "resets_at": ts},
				"seven_day": map[string]any{"utilization": 50.0, "resets_at": nil},
			})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write(body)
			}))
			defer srv.Close()
			withEndpoint(t, srv.URL)

			u, err := Fetch(context.Background(), "tok")
			if err != nil {
				t.Fatal(err)
			}
			if u.FiveHour.ResetsAt.IsZero() {
				t.Errorf("timestamp %q failed to parse", ts)
			}
		})
	}
}
