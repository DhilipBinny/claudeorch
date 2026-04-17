//go:build integration

package integration

import (
	"encoding/json"
	"os"
	"testing"
)

// setActiveInStore directly edits store.json to mark name as active.
// Used in tests that need to simulate a swap without running the swap command.
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
	m["active"] = name
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatalf("marshal store.json: %v", err)
	}
	if err := os.WriteFile(env.StoreFile(), out, 0o600); err != nil {
		t.Fatalf("write store.json: %v", err)
	}
}
