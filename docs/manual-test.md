# claudeorch — Manual E2E Test Checklist

Run this checklist before tagging a release candidate. Requires two real Claude Max accounts logged in at different times. All commands are run on the local machine against real `~/.claude/` state.

## Pre-flight

- [ ] `go build -o /tmp/claudeorch-test ./cmd/claudeorch`
- [ ] `alias co=/tmp/claudeorch-test`
- [ ] Verify Claude Code is installed: `claude --version`
- [ ] Confirm starting from a clean state: `rm -rf ~/.claudeorch` (backs up nothing — double-check)

---

## 1. add

### 1a. Add first account (work)

1. `claude /login` → log in with account 1 (work email)
2. `co add work` → should detect identity from `~/.claude/.claude.json`, save profile, print confirmation
3. `co list` → should show `work` profile, no usage data (no usage endpoint hit yet)

Expected: `~/.claudeorch/profiles/work/credentials.json` and `claude.json` exist at mode `0600`.

### 1b. Add second account (home)

1. `claude /logout && claude /login` → log in with account 2 (home email)
2. `co add home` → saves second profile
3. `co list` → two profiles listed; `home` shown as active (the one currently in `~/.claude/`)

### 1c. Duplicate detection

1. Without logging out, `co add home2` → because identity matches `home`, should detect duplicate and refresh-in-place (update credentials, not create new profile)
2. Confirm no `home2` profile in `co list`

---

## 2. list

- [ ] `co list` → two rows; active profile marked with `*`; usage bars rendered (or `-` if unreachable)
- [ ] `co list --no-usage` → two rows, usage columns all `-`, no network call made
- [ ] `co list --json` → valid JSON, machine-readable

---

## 3. status

- [ ] `co status` → shows active profile name + email
- [ ] Open a second terminal, run `claude` (starts a session), then `co status` → session count > 0

---

## 4. swap

### 4a. Happy path

1. Confirm no Claude sessions are running (`co status`)
2. `co swap work` → should atomically swap credentials
3. `co list` → `work` is now active
4. `co swap home` → swap back

### 4b. Refuse with active session

1. Start `claude` in another terminal (do not exit)
2. `co swap work` → should refuse with clear error (exit 2)
3. `co swap --force work` → should warn and proceed
4. Exit the `claude` session

### 4c. Crash recovery (simulated)

1. `co swap work` — interrupt mid-swap with `kill -9` (requires timing; attempt if feasible)
2. `co doctor` → should report any orphaned `.pre-swap` files
3. `co swap work` again → should succeed (recover cleans up on next invocation)

---

## 5. launch

### 5a. Parallel sessions

1. Terminal 1: `co launch work` → Claude opens with work account; `echo $CLAUDE_CONFIG_DIR` shows isolate path
2. Terminal 2: `co launch home` → Claude opens with home account; different `CLAUDE_CONFIG_DIR`
3. Both sessions active simultaneously — confirm `co status` shows 2 sessions

### 5b. Isolated mode

1. `co launch --isolated work` → no symlinks to shared `CLAUDE.md` or `projects/` (fresh dir)

### 5c. Passthrough args

1. `co launch work -- --version` → should print Claude version (passthrough of `--version` to `claude`)

---

## 6. refresh

- [ ] `co refresh work` → fetches new access token, writes back to profile
- [ ] `co list` → no `!` marker on work profile (NeedsReauth not set)
- [ ] With `work` active: `co refresh work` → refused unless `--force`
- [ ] Simulate expired refresh token (edit credentials.json to have invalid `refreshToken`): `co refresh work` → should set `needs_reauth: true` and print guidance

---

## 7. doctor

- [ ] `co doctor` → all checks pass on a clean install
- [ ] Manually `chmod 644 ~/.claudeorch/profiles/work/credentials.json`
- [ ] `co doctor` → reports wrong permissions on `work` credentials
- [ ] `co doctor --fix` → repairs permissions, re-check shows green
- [ ] Create fake orphan: `touch ~/.claudeorch/.credentials.json.pre-swap`
- [ ] `co doctor` → reports pre-swap orphan

---

## 8. remove + rename

- [ ] `co rename work work2` → profile renamed; `co list` shows `work2`
- [ ] `co rename work2 work` → rename back
- [ ] `co remove home` → with no sessions: should prompt for confirmation; decline → profile still exists
- [ ] `co remove --force home` → removes home profile; `co list` shows only `work`
- [ ] Re-add home: `claude /logout && claude /login` (home) → `co add home`

---

## 9. purge

> Do this last — it destroys all claudeorch state.

- [ ] `co purge` (non-interactive, no TTY flags) → should error "requires interactive confirmation"
- [ ] `co purge --force --yes` → wipes `~/.claudeorch/` entirely; `co list` fails with "no store.json"
- [ ] Confirm `~/.claude/` still exists (purge must never touch it)

---

## 10. Global flags

- [ ] `co --debug list` → extra slog output on stderr; no raw tokens visible
- [ ] `co --no-color list` → plain text, no ANSI codes
- [ ] `co --json list` → JSON output
- [ ] `co --force swap work` (with running session) → swaps with warning, not refused

---

## Pass/Fail sign-off

| Section | Pass | Notes |
|---------|------|-------|
| 1. add  |      |       |
| 2. list |      |       |
| 3. status |    |       |
| 4. swap |      |       |
| 5. launch |    |       |
| 6. refresh |   |       |
| 7. doctor |    |       |
| 8. remove/rename | |   |
| 9. purge |     |       |
| 10. global flags | |   |

All sections pass → tag `v0.1.0-rc1`.
