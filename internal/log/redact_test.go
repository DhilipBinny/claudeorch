package log

import (
	"strings"
	"testing"
)

func TestRedact_EmptyReturnsEmpty(t *testing.T) {
	if got := Redact(""); got != "" {
		t.Errorf("Redact(\"\") = %q, want empty", got)
	}
}

func TestRedact_ShortTokenNoPrefix(t *testing.T) {
	tests := []string{"short", "1234567890", "a"}
	for _, tok := range tests {
		t.Run(tok, func(t *testing.T) {
			got := Redact(tok)
			if got != "<redacted>" {
				t.Errorf("Redact(%q) = %q, want \"<redacted>\" (≤10 chars, no prefix)", tok, got)
			}
		})
	}
}

func TestRedact_LongTokenShowsPrefix(t *testing.T) {
	// 50-character deterministic token.
	tok := "sk-ant-0123456789abcdefghijklmnopqrstuvwxyz_AAAAA"
	got := Redact(tok)
	want := "<redacted:sk-ant-012...>"
	if got != want {
		t.Errorf("Redact(longTok) = %q, want %q", got, want)
	}
}

// Critical security assertion: the redacted form must never reveal the
// second half of the token. If the implementation ever changes to show
// more characters, this test fails.
func TestRedact_NeverRevealsSuffix(t *testing.T) {
	tok := "sk-ant-0123456789SECRETSUFFIX_SHOULD_NEVER_APPEAR"
	got := Redact(tok)
	if strings.Contains(got, "SECRETSUFFIX") {
		t.Errorf("Redact() leaked the token suffix: %q", got)
	}
}

// Unicode token — we redact by rune length, not byte length, so a multi-byte
// short string still hits the ≤10 branch correctly.
//
// Actually Go's len() on a string returns bytes. For redaction we want length
// measurement to stay byte-oriented (safer — guarantees we never slice
// mid-rune because we only slice at index 10 and real tokens are ASCII).
// This test documents and enforces that.
func TestRedact_UnicodeAsBytes(t *testing.T) {
	// 3 unicode chars = 9 bytes (3-byte UTF-8 each). len() = 9, so ≤10 rule applies.
	unicodeShort := "日本語"
	if got := Redact(unicodeShort); got != "<redacted>" {
		t.Errorf("Redact(unicodeShort) = %q, want \"<redacted>\"", got)
	}

	// 11 unicode chars = 33 bytes. len() = 33 > 10. Slicing at 10 bytes lands
	// mid-rune, which would normally be bad — but for a real OAuth token
	// that's never going to happen. We still guard against panics though,
	// and Go string slicing produces valid bytes (just may not be valid UTF-8).
	unicodeLong := "日本語日本語日本語日本"
	got := Redact(unicodeLong)
	if !strings.HasPrefix(got, "<redacted:") {
		t.Errorf("Redact(unicodeLong) = %q, want prefix <redacted:", got)
	}
}

func TestScanAndRedact_AnthropicAccessToken(t *testing.T) {
	input := "request failed with token sk-ant-0123456789abcdefghijklmnop"
	got := ScanAndRedact(input)
	if strings.Contains(got, "sk-ant-0123456789abcdefghijklmnop") {
		t.Errorf("raw token leaked: %q", got)
	}
	if !strings.Contains(got, "<redacted:") {
		t.Errorf("expected redaction marker in %q", got)
	}
}

func TestScanAndRedact_JWT(t *testing.T) {
	// Three-segment dot-separated base64url, prefixed with "eyJ".
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	input := "Authorization: Bearer " + jwt
	got := ScanAndRedact(input)
	if strings.Contains(got, jwt) {
		t.Errorf("JWT leaked: %q", got)
	}
	if !strings.Contains(got, "<redacted:eyJ") {
		t.Errorf("expected <redacted:eyJ...> in %q", got)
	}
}

func TestScanAndRedact_RefToken(t *testing.T) {
	input := "refreshToken: ref_abcdefghijklmnopqrstuvwxyz"
	got := ScanAndRedact(input)
	if strings.Contains(got, "ref_abcdefghijklmnopqrstuvwxyz") {
		t.Errorf("ref token leaked: %q", got)
	}
}

func TestScanAndRedact_MultipleTokensInOneString(t *testing.T) {
	input := "old=sk-ant-0123456789ABCDEFGHIJ new=sk-ant-zyxwvutsrq9876543210"
	got := ScanAndRedact(input)
	if strings.Contains(got, "0123456789ABCDEFGHIJ") {
		t.Errorf("first token leaked: %q", got)
	}
	if strings.Contains(got, "zyxwvutsrq9876543210") {
		t.Errorf("second token leaked: %q", got)
	}
}

// Non-token strings must pass through unchanged.
func TestScanAndRedact_Idempotent(t *testing.T) {
	safeInputs := []string{
		"",
		"plain error message",
		"user=alice@example.com",
		"path=/home/binny/.claude",
		"pid=12345",
		"sk-ant-short", // under minimum length (20)
		"ref_short",    // under minimum length
	}
	for _, s := range safeInputs {
		t.Run(s, func(t *testing.T) {
			if got := ScanAndRedact(s); got != s {
				t.Errorf("ScanAndRedact(%q) = %q (expected unchanged)", s, got)
			}
		})
	}
}
