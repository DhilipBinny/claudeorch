//go:build integration

package integration

import (
	"encoding/json"
	"os"
	"testing"
)

// setActiveInStore directly edits store.json to mark name as active.
// Used in tests that need to simulate a swap without running the swap command.
//
// v0.2.0+ schema: sets the named profile's location to "live" and every
// other profile's location to "dormant". The top-level "active" field is
// removed (not serialized in v2).
func setActiveInStore(t *testing.T, env *Env, name string) {
	t.Helper()
	data, err := os.ReadFile(env.StoreFile())
	if err != nil {
		t.Fatalf("read store.json: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse store.json: %v", err)
	}
	delete(m, "active") // v1 field, no longer serialized
	if profs, ok := m["profiles"].(map[string]any); ok {
		for pname, raw := range profs {
			if p, ok := raw.(map[string]any); ok {
				if pname == name {
					p["location"] = "live"
				} else {
					p["location"] = "dormant"
				}
			}
		}
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatalf("marshal store.json: %v", err)
	}
	if err := os.WriteFile(env.StoreFile(), out, 0o600); err != nil {
		t.Fatalf("write store.json: %v", err)
	}
}

// clearActiveInStore forces every profile's Location to "dormant", simulating
// a state where profiles exist but none is live (edge case for status
// rendering).
//
// v0.2.0+ schema keeps Active pointer derived from per-profile Location, so
// we have to touch each profile record directly. v1 compat: deletes the
// top-level "active" field too.
func clearActiveInStore(t *testing.T, env *Env) {
	t.Helper()
	data, err := os.ReadFile(env.StoreFile())
	if err != nil {
		t.Fatalf("read store.json: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse store.json: %v", err)
	}
	delete(m, "active")
	if profs, ok := m["profiles"].(map[string]any); ok {
		for _, raw := range profs {
			if p, ok := raw.(map[string]any); ok {
				p["location"] = "dormant"
			}
		}
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatalf("marshal store.json: %v", err)
	}
	if err := os.WriteFile(env.StoreFile(), out, 0o600); err != nil {
		t.Fatalf("write store.json: %v", err)
	}
}
