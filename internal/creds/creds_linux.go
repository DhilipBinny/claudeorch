//go:build linux

package creds

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DhilipBinny/claudeorch/internal/fsio"
)

// Claude Code on Linux stores its OAuth tokens in the freedesktop Secret
// Service (gnome-keyring, kwallet, KeePassXC, ...) rather than in
// ~/.claude/.credentials.json. The bundled binary links libsecret-1.so.0
// and ships node-keytar, which registers items under the generic keytar
// schema with two attributes:
//
//	service = "Claude Code-credentials"   (OAuth blob, same JSON as macOS)
//	account = the OS username
//
// This mirrors the macOS Keychain layout documented in creds_darwin.go, so
// the two platforms differ only in which CLI we shell out to: 'security'
// there, 'secret-tool' here.
//
// The flat file is still consulted as a fallback: CLAUDE_CONFIG_DIR setups,
// headless/SSH boxes with no D-Bus session, and API-key installs may keep
// credentials on disk.
const (
	// secretCredsService is the Secret Service 'service' attribute holding
	// the OAuth credential blob.
	secretCredsService = "Claude Code-credentials"

	// secretAPIKeyService is the 'service' attribute holding a bare API key
	// (sk-ant-...). The value is a raw string, not JSON.
	secretAPIKeyService = "Claude Code"
)

// secretToolTimeout bounds every secret-tool invocation. A locked keyring
// can pop an unlock prompt and block forever; claudeorch is a CLI and must
// not hang, so we time out and fall back to the flat file instead.
const secretToolTimeout = 5 * time.Second

// credsServiceCandidates are the 'service' attribute values we probe when
// looking for the OAuth blob, in priority order. Claude Code has shipped
// more than one name across versions, and probing costs one cheap exec.
var credsServiceCandidates = []string{secretCredsService, secretAPIKeyService}

// secretServiceDisabled reports whether the Secret Service must not be
// touched, forcing flat-file mode. True in two cases, matching darwin:
//
//   - Inside any test binary (testing.Testing()). Load-bearing: without it
//     a test reaching ReadLive/WriteLive would read — or destroy — the
//     developer's real Claude Code keyring entry.
//   - CLAUDEORCH_NO_KEYCHAIN is set (to anything but "0"/"false"), for
//     spawned binaries in integration tests and CI where testing.Testing()
//     is false.
func secretServiceDisabled() bool {
	if testing.Testing() {
		return true
	}
	v := os.Getenv("CLAUDEORCH_NO_KEYCHAIN")
	return v != "" && v != "0" && v != "false"
}

var (
	secretToolOnce sync.Once
	secretToolBin  string
)

// secretToolAvailable reports whether we can talk to the Secret Service:
// the secret-tool binary must exist and a D-Bus session must be reachable.
// Without DBUS_SESSION_BUS_ADDRESS (bare SSH, cron) libsecret cannot
// autolaunch, so we skip straight to the flat file.
func secretToolAvailable() bool {
	if secretServiceDisabled() {
		return false
	}
	secretToolOnce.Do(func() {
		if p, err := exec.LookPath("secret-tool"); err == nil {
			secretToolBin = p
		}
	})
	if secretToolBin == "" {
		return false
	}
	return os.Getenv("DBUS_SESSION_BUS_ADDRESS") != "" || os.Getenv("XDG_RUNTIME_DIR") != ""
}

// currentUsername returns the OS username used as the keyring 'account'
// attribute. Claude Code uses the OS username, not the Anthropic email.
func currentUsername() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "unknown"
}

// runSecretTool executes secret-tool with the given args, feeding stdin if
// non-nil, and returns stdout. Always bounded by secretToolTimeout.
func runSecretTool(stdin []byte, args ...string) ([]byte, error) {
	if !secretToolAvailable() {
		return nil, errors.New("secret service unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), secretToolTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, secretToolBin, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("secret-tool timed out after %s (keyring locked?)", secretToolTimeout)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("secret-tool %s: %s", args[0], msg)
	}
	return stdout.Bytes(), nil
}

// lookupSecret fetches the secret stored under service/account. An empty
// account omits the attribute entirely, which makes libsecret match on
// service alone and return the first hit — this rescues installs where the
// account attribute is not the OS username.
func lookupSecret(service, account string) ([]byte, error) {
	args := []string{"lookup", "service", service}
	if account != "" {
		args = append(args, "account", account)
	}
	out, err := runSecretTool(nil, args...)
	if err != nil {
		return nil, err
	}
	// secret-tool emits the secret verbatim with no trailing newline, but
	// trim defensively: a stray newline would corrupt an API key.
	out = bytes.TrimSpace(out)
	if len(out) == 0 {
		return nil, fmt.Errorf("secret-tool: empty value for service %q", service)
	}
	return out, nil
}

// storeSecret writes (or replaces) the secret under service/account.
// secret-tool store reads the secret from stdin, never from argv, so it
// never lands in the process table.
func storeSecret(service, account, label string, data []byte) error {
	_, err := runSecretTool(data,
		"store", "--label="+label,
		"service", service,
		"account", account,
	)
	return err
}

// clearSecret removes the item under service/account. A missing item is not
// an error — secret-tool clear exits 0 when nothing matches.
func clearSecret(service, account string) error {
	_, err := runSecretTool(nil, "clear", "service", service, "account", account)
	return err
}

// credsLocation is a resolved (service, account) pair in the Secret Service.
type credsLocation struct {
	service string
	account string
}

// findCredsLocation probes the candidate service names for an existing
// OAuth blob and returns where it actually lives, so WriteLive updates the
// same item Claude Code reads rather than creating a parallel one. When
// nothing is found it returns the canonical default.
func findCredsLocation() (loc credsLocation, data []byte, found bool) {
	user := currentUsername()
	for _, svc := range credsServiceCandidates {
		for _, acct := range []string{user, ""} {
			out, err := lookupSecret(svc, acct)
			if err != nil || !looksLikeJSONObject(out) {
				continue
			}
			// Record the concrete account we matched. When acct was
			// empty we matched on service alone, so fall back to the
			// username for future writes.
			resolved := acct
			if resolved == "" {
				resolved = user
			}
			return credsLocation{service: svc, account: resolved}, out, true
		}
	}
	return credsLocation{service: secretCredsService, account: user}, nil, false
}

// looksLikeJSONObject reports whether data is a JSON object. Guards against
// treating a bare API key string (stored under the "Claude Code" service)
// as an OAuth credential blob.
func looksLikeJSONObject(data []byte) bool {
	return bytes.HasPrefix(bytes.TrimSpace(data), []byte("{"))
}

// ReadLive reads Claude Code's current OAuth credentials.
//
// Strategy:
//  1. Secret Service via secret-tool (where Claude Code 2.x keeps them).
//  2. Flat file at credsPath (CLAUDE_CONFIG_DIR, headless, manual setups).
//  3. Otherwise a descriptive error with recovery steps.
func ReadLive(credsPath string) ([]byte, error) {
	if secretToolAvailable() {
		if _, data, found := findCredsLocation(); found {
			return data, nil
		}
	}

	fileData, fileErr := os.ReadFile(credsPath)
	if fileErr == nil && len(bytes.TrimSpace(fileData)) > 0 {
		return fileData, nil
	}

	if secretToolAvailable() {
		return nil, fmt.Errorf(
			"no credentials found — tried the Secret Service (service %q, account %q)\n"+
				"and the flat file at %s.\n\n"+
				"Are you logged in to Claude Code? Run 'claude /login' first.\n\n"+
				"If your keyring is locked, unlock it and retry. To force flat-file\n"+
				"mode instead, set CLAUDEORCH_NO_KEYCHAIN=1.",
			secretCredsService, currentUsername(), credsPath)
	}
	if os.IsNotExist(fileErr) {
		return nil, fmt.Errorf(
			"no credentials found at %s and no Secret Service available\n"+
				"(secret-tool missing, or no D-Bus session) — are you logged in to Claude Code?",
			credsPath)
	}
	return nil, fmt.Errorf("read credentials %s: %w", credsPath, fileErr)
}

// WriteLive writes OAuth credentials to the live location.
//
// Writes to the Secret Service (the location Claude Code actually reads)
// and merges into the flat file as a fallback. Either alone is sufficient
// for Claude Code to function, so only a double failure is fatal.
func WriteLive(credsPath string, data []byte) error {
	if !secretToolAvailable() {
		return mergeIntoFile(credsPath, data)
	}

	loc, _, _ := findCredsLocation()
	secretErr := storeSecret(loc.service, loc.account, "Claude Code credentials", data)
	fileErr := mergeIntoFile(credsPath, data)

	if secretErr != nil && fileErr != nil {
		return fmt.Errorf(
			"could not write credentials to the keyring (%v) or file (%v)\n\n"+
				"If your keyring is locked, unlock it and retry, or set\n"+
				"CLAUDEORCH_NO_KEYCHAIN=1 to use the flat file only",
			secretErr, fileErr)
	}
	return nil
}

// IsKeychainBased reports whether this platform stores live credentials in
// an OS-managed secret store. True on Linux when a Secret Service is
// reachable; false when secret-tool or D-Bus is missing, or when flat-file
// mode is forced via CLAUDEORCH_NO_KEYCHAIN.
func IsKeychainBased() bool {
	return secretToolAvailable()
}

// credentialsPath returns the default credentials file path on Linux.
func credentialsPath() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, ".credentials.json")
	}
	return filepath.Join(os.Getenv("HOME"), ".claude", ".credentials.json")
}

// ReadLiveAPIKey reads the API key, preferring the Secret Service and
// falling back to {"apiKey": "sk-ant-..."} in the flat file.
func ReadLiveAPIKey() (string, error) {
	if secretToolAvailable() {
		for _, acct := range []string{currentUsername(), ""} {
			if out, err := lookupSecret(secretAPIKeyService, acct); err == nil {
				if key := extractAPIKey(out); key != "" {
					return key, nil
				}
			}
		}
	}

	if data, err := os.ReadFile(credentialsPath()); err == nil {
		var envelope struct {
			APIKey string `json:"apiKey"`
		}
		if err := json.Unmarshal(data, &envelope); err == nil {
			if key := strings.TrimSpace(envelope.APIKey); key != "" {
				return key, nil
			}
		}
	}

	// Neither the keyring nor .credentials.json holds a key. Claude Code
	// also authenticates from ANTHROPIC_API_KEY and from primaryApiKey in
	// .claude.json, which is where a Console login without a subscription
	// puts the key it provisions. Consult those before giving up.
	if key, _, ok := liveAPIKeyFromClaudeState(); ok {
		return key, nil
	}

	if EnvAPIKeyRejected() {
		return "", fmt.Errorf(
			"no usable API key found: ANTHROPIC_API_KEY is set but you declined it\n" +
				"in Claude Code, and no other key is stored. Approve that key or run\n" +
				"'claude /login' so Claude Code provisions one")
	}
	return "", fmt.Errorf("no API key found in the keyring, %s, ANTHROPIC_API_KEY, or .claude.json", credentialsPath())
}

// extractAPIKey pulls a key out of a keyring value that may be either a
// bare string or a JSON envelope, depending on how it was stored.
func extractAPIKey(data []byte) string {
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "{") {
		var envelope struct {
			APIKey string `json:"apiKey"`
		}
		if err := json.Unmarshal([]byte(trimmed), &envelope); err == nil {
			return strings.TrimSpace(envelope.APIKey)
		}
		return ""
	}
	return trimmed
}

// WriteLiveAPIKey writes the API key to the Secret Service and mirrors it
// into the flat file.
func WriteLiveAPIKey(apiKey string) error {
	path := credentialsPath()
	blob, err := json.Marshal(map[string]string{"apiKey": apiKey})
	if err != nil {
		return fmt.Errorf("marshal API key: %w", err)
	}

	if !secretToolAvailable() {
		return mergeIntoFile(path, blob)
	}

	secretErr := storeSecret(secretAPIKeyService, currentUsername(), "Claude Code API key", []byte(apiKey))
	fileErr := mergeIntoFile(path, blob)
	if secretErr != nil && fileErr != nil {
		return fmt.Errorf("could not write API key to the keyring (%v) or file (%v)", secretErr, fileErr)
	}
	return nil
}

// DeleteLiveCredentials removes the OAuth credentials so a subsequent
// API-key swap is not shadowed by stale tokens. Clears both the keyring
// item and the claudeAiOauth key in the flat file.
func DeleteLiveCredentials() error {
	var secretErr error
	if secretToolAvailable() {
		loc, _, found := findCredsLocation()
		if found {
			secretErr = clearSecret(loc.service, loc.account)
		}
	}
	fileErr := deleteFileKeys(credentialsPath(), "claudeAiOauth")
	if secretErr != nil {
		return secretErr
	}
	return fileErr
}

// DeleteLiveAPIKey removes the API key from the keyring and the flat file.
func DeleteLiveAPIKey() error {
	var secretErr error
	if secretToolAvailable() {
		secretErr = clearSecret(secretAPIKeyService, currentUsername())
	}
	fileErr := deleteFileKeys(credentialsPath(), "apiKey")
	if secretErr != nil {
		return secretErr
	}
	return fileErr
}

// mergeIntoFile merges incoming top-level keys into the existing
// credentials file, preserving keys claudeorch does not own.
//
// This matters: a real ~/.claude/.credentials.json also holds mcpOAuth
// tokens for MCP servers and plugins. Clobbering the file on every swap
// would silently log the user out of every MCP integration they have.
// The schema package deliberately treats unknown fields as opaque; this
// keeps that promise on the write path.
func mergeIntoFile(path string, incoming []byte) error {
	var incomingMap map[string]json.RawMessage
	if err := json.Unmarshal(incoming, &incomingMap); err != nil {
		// Not a JSON object — nothing to merge, write it through as-is.
		return fsio.WriteFileAtomic(path, incoming, 0o600)
	}

	merged := map[string]json.RawMessage{}
	if existing, err := os.ReadFile(path); err == nil && len(bytes.TrimSpace(existing)) > 0 {
		if err := json.Unmarshal(existing, &merged); err != nil {
			// Existing file is not a JSON object (corrupt or foreign
			// format). Preserving it would mean merging into garbage, so
			// start clean rather than fail the swap.
			merged = map[string]json.RawMessage{}
		}
	}
	for k, v := range incomingMap {
		merged[k] = v
	}

	out, err := json.Marshal(merged)
	if err != nil {
		return fmt.Errorf("marshal merged credentials: %w", err)
	}
	return fsio.WriteFileAtomic(path, out, 0o600)
}

// deleteFileKeys removes the named top-level keys from the credentials
// file, leaving every other key intact. A missing file is not an error.
func deleteFileKeys(path string, keys ...string) error {
	existing, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read credentials %s: %w", path, err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(existing, &m); err != nil {
		// Cannot parse, so cannot surgically remove. Leave it alone.
		return nil
	}
	changed := false
	for _, k := range keys {
		if _, ok := m[k]; ok {
			delete(m, k)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	out, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal credentials: %w", err)
	}
	return fsio.WriteFileAtomic(path, out, 0o600)
}
