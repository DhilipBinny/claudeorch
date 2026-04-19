package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/DhilipBinny/claudeorch/internal/fsio"
	"github.com/DhilipBinny/claudeorch/internal/paths"
	"github.com/DhilipBinny/claudeorch/internal/profile"
	"github.com/DhilipBinny/claudeorch/internal/schema"
	"github.com/spf13/cobra"
)

func init() {
	registerSubcommand(func(root *cobra.Command) {
		root.AddCommand(newAddCmd())
	})
}

func newAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add [name]",
		Short: "Add the currently logged-in Claude account as a named profile.",
		Long: `Reads the active Claude Code credentials and saves them as a profile.

GOLDEN RULE: run 'claudeorch add' BEFORE 'claude /logout' or '/login' for a
different account. 'claude /logout' deletes the local OAuth tokens. If you
haven't saved them here first, they're gone and you'll have to re-authenticate
through the browser to get that account back.

Behaviour:
  - If the live account is not yet saved, creates a new profile with NAME.
  - If the live account is already saved under the SAME name, refreshes it
    in place.
  - If the live account is already saved under a DIFFERENT name, refuses
    with a clear error (the explicit NAME arg would otherwise be silently
    ignored).

The newly-saved profile is marked active automatically — it IS the live
account on disk, so 'status' reflects that without needing a separate 'swap'.

NAME is optional. When omitted on an interactive terminal you'll be prompted;
on a non-interactive terminal the email prefix is used as the default name.`,
		Args:          cobra.MaximumNArgs(1),
		RunE:          runAdd,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
}

func runAdd(cmd *cobra.Command, args []string) error {
	// Validate the explicit name up-front, before any disk reads. Otherwise a
	// garbage name like "../bad" silently passes through because the duplicate
	// check earlier takes over — refresh-in-place runs, ignoring the name, and
	// the user never learns their input was wrong.
	if len(args) > 0 {
		if err := paths.ValidateProfileName(args[0]); err != nil {
			return err
		}
	}

	// Read live Claude state.
	claudeJSONPath, err := paths.ClaudeJSONPath()
	if err != nil {
		return err
	}
	credsPath, err := paths.ClaudeCredentialsPath()
	if err != nil {
		return err
	}

	claudeJSONData, err := os.ReadFile(claudeJSONPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no .claude.json found at %s — are you logged in to Claude Code?", claudeJSONPath)
		}
		return fmt.Errorf("read %s: %w", claudeJSONPath, err)
	}

	credsData, err := os.ReadFile(credsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no credentials found at %s — are you logged in to Claude Code?", credsPath)
		}
		return fmt.Errorf("read %s: %w", credsPath, err)
	}

	identity, err := schema.ExtractIdentity(claudeJSONData)
	if err != nil {
		return fmt.Errorf("parse .claude.json: %w", err)
	}
	if _, err := schema.ParseCredentials(credsData); err != nil {
		return fmt.Errorf("parse .credentials.json: %w", err)
	}

	// Acquire global lock.
	lockPath, err := paths.LockFile()
	if err != nil {
		return err
	}
	if err := fsio.EnsureDir(filepath.Dir(lockPath), 0o700); err != nil {
		return err
	}
	release, err := fsio.AcquireLock(context.Background(), lockPath)
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer func() { _ = release() }()

	storePath, err := paths.StoreFile()
	if err != nil {
		return err
	}
	store, err := profile.Load(storePath)
	if err != nil {
		return fmt.Errorf("load store: %w", err)
	}

	// Reconcile first — if the live account we're about to snapshot already
	// matches an existing profile whose saved tokens are older than live,
	// reconcile will catch that and we won't create a misleading "duplicate"
	// snapshot with partially-stale data.
	if _, err := reconcileProfiles(store, cmd.ErrOrStderr()); err != nil {
		return fmt.Errorf("reconcile: %w", err)
	}

	// Duplicate check: same (email, orgUUID) already saved.
	if existingName, found := profile.Resolve(store, identity.EmailAddress, identity.OrganizationUUID); found {
		// If the user provided an explicit name that disagrees with the matching
		// profile's name, refuse with a clear message. Silently using existingName
		// and ignoring the user's arg is a footgun — they typed 'add bala'
		// expecting bala to be saved, but live ~/.claude/ actually holds dhilip,
		// so we'd refresh dhilip instead without telling them what happened.
		if len(args) > 0 && args[0] != existingName {
			return fmt.Errorf(
				"live ~/.claude/ holds account %s, which is already saved as %q — "+
					"not %q\n\n"+
					"To save a different account as %q, first log in as that account:\n"+
					"  claude /logout && claude /login\n"+
					"  claudeorch add %s\n\n"+
					"To refresh the existing %q profile, run:\n"+
					"  claudeorch add %s\n"+
					"  # or: claudeorch refresh %s",
				identity.EmailAddress, existingName, args[0],
				args[0], args[0], existingName, existingName, existingName)
		}

		profileDir, err := paths.ProfileDir(existingName)
		if err != nil {
			return err
		}
		if err := fsio.WriteFileAtomic(filepath.Join(profileDir, "credentials.json"), credsData, 0o600); err != nil {
			return fmt.Errorf("update credentials: %w", err)
		}
		if err := fsio.WriteFileAtomic(filepath.Join(profileDir, "claude.json"), claudeJSONData, 0o600); err != nil {
			return fmt.Errorf("update claude.json: %w", err)
		}
		now := time.Now().UTC()
		store.Profiles[existingName].LastUsedAt = now
		// 'add' always reads from live ~/.claude/, so whatever profile just
		// absorbed that identity IS the one currently live. Keep the store's
		// 'active' pointer in sync with reality.
		if setErr := store.SetActive(existingName); setErr != nil {
			return fmt.Errorf("set active: %w", setErr)
		}
		if err := store.Save(storePath); err != nil {
			return fmt.Errorf("save store: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Updated credentials for existing profile %q (%s)\n", existingName, identity.EmailAddress)
		return nil
	}

	// Determine name for new profile.
	var name string
	if len(args) > 0 {
		name = args[0]
	} else if stdinIsTerminal() {
		name, err = promptProfileName(cmd, identity.EmailAddress, store)
		if err != nil {
			return err
		}
	} else {
		name, err = emailPrefixName(identity.EmailAddress, store)
		if err != nil {
			return err
		}
	}

	if err := paths.ValidateProfileName(name); err != nil {
		return err
	}
	if _, exists := store.Profiles[name]; exists {
		return fmt.Errorf("profile %q already exists (use a different name)", name)
	}

	// Write profile to disk.
	profileDir, err := paths.ProfileDir(name)
	if err != nil {
		return err
	}
	if err := fsio.EnsureDir(profileDir, 0o700); err != nil {
		return err
	}
	if err := fsio.WriteFileAtomic(filepath.Join(profileDir, "credentials.json"), credsData, 0o600); err != nil {
		return fmt.Errorf("write credentials: %w", err)
	}
	if err := fsio.WriteFileAtomic(filepath.Join(profileDir, "claude.json"), claudeJSONData, 0o600); err != nil {
		return fmt.Errorf("write claude.json: %w", err)
	}

	// Register in store.
	store.Profiles[name] = &profile.Profile{
		Name:             name,
		Email:            identity.EmailAddress,
		OrganizationUUID: identity.OrganizationUUID,
		OrganizationName: identity.OrganizationName,
		CreatedAt:        time.Now().UTC(),
		Source:           profile.SourceOAuth,
	}
	// The credentials we just copied came from live ~/.claude/, so this new
	// profile IS the live account. Mark it active so 'status' and 'list'
	// reflect reality out of the box.
	if setErr := store.SetActive(name); setErr != nil {
		return fmt.Errorf("set active: %w", setErr)
	}
	if err := store.Save(storePath); err != nil {
		return fmt.Errorf("save store: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Added profile %q (%s)\n", name, identity.EmailAddress)
	return nil
}

// promptProfileName asks the user for a profile name on the terminal.
// The default (shown in brackets) is the email-prefix with collision avoidance.
func promptProfileName(cmd *cobra.Command, email string, store *profile.Store) (string, error) {
	defaultName, _ := emailPrefixName(email, store)
	fmt.Fprintf(cmd.ErrOrStderr(), "Profile name [%s]: ", defaultName)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read profile name: %w", err)
	}
	name := strings.TrimSpace(line)
	if name == "" {
		name = defaultName
	}
	return name, nil
}

// nonAlnum matches characters that are not alphanumeric, hyphen, or underscore.
var nonAlnum = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// emailPrefixName derives a safe profile name from the email's local part,
// appending a numeric suffix if there's a collision.
func emailPrefixName(email string, store *profile.Store) (string, error) {
	local := email
	if at := strings.IndexByte(email, '@'); at >= 0 {
		local = email[:at]
	}
	// Sanitize: replace any run of unsafe chars with a single hyphen.
	name := nonAlnum.ReplaceAllString(local, "-")
	// Trim leading/trailing hyphens.
	name = strings.Trim(name, "-")
	if name == "" {
		name = "profile"
	}
	// Truncate to 60 chars so suffix fits within 64-char limit.
	if len(name) > 60 {
		name = name[:60]
	}
	// Ensure it starts with alphanumeric (already guaranteed by trim above, but be safe).
	if len(name) > 0 && !isAlnum(name[0]) {
		name = "p" + name
	}

	base := name
	for i := 2; ; i++ {
		if _, exists := store.Profiles[name]; !exists {
			return name, nil
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
}

func isAlnum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
