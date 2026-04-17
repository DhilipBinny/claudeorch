// Package log provides structured logging with built-in credential redaction.
//
// Everything written to our log — stderr or file — must pass through these
// helpers for any field that might hold an OAuth token, refresh token, or
// other secret. Leaking a token to disk is a security incident.
//
// The redaction model:
//
//   - Short tokens (≤10 chars, or empty): replaced with "<redacted>" or "".
//   - Long tokens: replaced with "<redacted:<first-10-chars>...>" so operators
//     can correlate log lines referring to the same token without exposing it.
//
// 10 characters is short enough to be useless to an attacker (far less than
// the ~108 chars of a real OAuth token) but long enough that two different
// tokens virtually never share a prefix, keeping correlation useful.
package log

import (
	"regexp"
)

// Redact returns a safe, non-reversible representation of a secret string.
//
// Empty input returns "". Strings ≤10 chars return "<redacted>" with no
// prefix (too short to be safely correlatable). Longer strings return
// "<redacted:<first-10-chars>...>" for debugging.
func Redact(token string) string {
	if token == "" {
		return ""
	}
	if len(token) <= 10 {
		return "<redacted>"
	}
	return "<redacted:" + token[:10] + "...>"
}

// tokenPatterns describes byte patterns we'll aggressively redact if they
// appear in free-form text (error messages, response bodies, etc.). The set
// is deliberately small and well-anchored — each pattern targets a known
// token format we can identify with high confidence.
//
// Anchored on a prefix + a long run of token-legal characters. We require at
// least 20 characters of payload because real tokens are far longer than
// that; shorter strings with these prefixes are likely not secrets we need
// to redact.
var tokenPatterns = []*regexp.Regexp{
	// Anthropic access tokens: "sk-ant-..." followed by ≥20 token-legal chars.
	regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{20,}`),
	// Generic sk- prefixed API keys (OpenAI-style, plus anything else Anthropic
	// or a downstream may adopt). Narrower prefix so we don't flag SSH ids.
	regexp.MustCompile(`sk-[A-Za-z0-9]{32,}`),
	// Plausible refresh-token shape Anthropic uses: "ref_" + long run.
	regexp.MustCompile(`ref_[A-Za-z0-9_\-]{20,}`),
	// JWTs: three base64url sections separated by dots. "eyJ..." is the
	// characteristic prefix (base64 of '{"').
	regexp.MustCompile(`eyJ[A-Za-z0-9_=\-]{5,}\.[A-Za-z0-9_=\-]{5,}\.[A-Za-z0-9_=\-]{5,}`),
}

// ScanAndRedact scans a free-form string for token-like patterns and
// replaces each match with the Redact()'d form.
//
// This is a belt-and-suspenders defense against accidental logging of
// response bodies, error strings, or user input that may contain tokens.
// Explicit redaction via Redact() at each known-secret call site is still
// the primary strategy — this is the safety net.
func ScanAndRedact(s string) string {
	for _, pat := range tokenPatterns {
		s = pat.ReplaceAllStringFunc(s, Redact)
	}
	return s
}
