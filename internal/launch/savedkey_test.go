package launch

import (
	"path/filepath"
	"testing"
)

func TestSavedAPIKeyMatches(t *testing.T) {
	cases := []struct {
		name    string
		file    string // .claude.json content; "" means no file
		key     string
		want    bool
		skipWrt bool
	}{
		{"same key", `{"primaryApiKey":"sk-ant-test123"}`, "sk-ant-test123", true, false},
		{"whitespace ignored", `{"primaryApiKey":" sk-ant-test123\n"}`, "sk-ant-test123", true, false},
		{"different key", `{"primaryApiKey":"sk-ant-other"}`, "sk-ant-test123", false, false},
		{"no saved key", `{"oauthAccount":{}}`, "sk-ant-test123", false, false},
		{"empty saved key", `{"primaryApiKey":""}`, "sk-ant-test123", false, false},
		{"empty profile key", `{"primaryApiKey":""}`, "", false, false},
		{"malformed json", `not json`, "sk-ant-test123", false, false},
		{"missing file", "", "sk-ant-test123", false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if !c.skipWrt {
				writeTestFile(t, filepath.Join(dir, ".claude.json"), c.file)
			}
			if got := SavedAPIKeyMatches(dir, c.key); got != c.want {
				t.Errorf("SavedAPIKeyMatches = %v, want %v", got, c.want)
			}
		})
	}
}
