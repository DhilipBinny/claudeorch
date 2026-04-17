# Commands reference

Full reference for every `claudeorch` subcommand. For a short description run `claudeorch <command> --help` — this file exists for browsing before installation.

## Global flags

| Flag | Effect |
|---|---|
| `--debug` | Verbose structured logs to stderr + `~/.claudeorch/log/claudeorch.log`. Tokens are redacted. Also enabled by `CLAUDEORCH_DEBUG=1`. |
| `--json` | Machine-readable output (where applicable, e.g. `list --json`). |
| `--no-color` | Disable ANSI colors. Also honors `NO_COLOR` env var and piped stdout. |
| `--force` | Override safety gates (swap with running sessions, remove active profile, etc.). Prints a warning. |
| `--version` | Print version + commit + build date. |
| `--help` | Command-specific help. |

## Profile management

### `claudeorch add [name]`

Snapshot the currently-active Claude Code credentials as a named profile. Reads `~/.claude/.credentials.json` and `~/.claude.json`, writes a copy under `~/.claudeorch/profiles/<name>/` at `0600`, and marks the new profile active in `store.json`.

**Golden rule:** run this BEFORE `claude /logout` or a re-`login`, or the tokens are gone.

**Name argument:**
- Provided → used literally, validated against `^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`.
- Omitted on a TTY → interactive prompt, defaults to the email local-part.
- Omitted on a non-TTY → email prefix is used with `-N` suffix for collision.

**Duplicate detection:**
- Same `(email, org_uuid)` already saved under the SAME name → refresh-in-place (updates creds).
- Same identity saved under a DIFFERENT name → refuses with a clear error (suggests `claude /logout` + `/login` as a different account first).

### `claudeorch list`

Table of every saved profile with live 5H + 7D usage bars fetched from `api.anthropic.com/api/oauth/usage`. One HTTP request per profile.

**Flags:**
- `--no-usage` skip the API calls (instant, shows `-` in usage columns).
- `--json` emit JSON instead of the table.

Columns: profile name, email, organization, 5H bar + % + reset, 7D bar + % + reset, status (active / needs-reauth).

### `claudeorch status`

"Right now" dashboard for the active profile:
- Active profile name + email
- 5H + 7D usage bars for the active profile (one API call)
- Running Claude sessions (terminal + IDE), each tagged with the profile it's using
- Footer teasing `list` when other profiles exist

**Flags:**
- `--no-usage` skip the API call.

### `claudeorch rename <old> <new>`

Rename a profile. Validates the new name, moves `profiles/<old>/` to `profiles/<new>/`, also moves `isolate/<old>/` to `isolate/<new>/` if it exists, and updates the active pointer if needed.

### `claudeorch remove <name>`

Delete a profile. Zero-overwrites every credential file in `profiles/<name>/` before unlinking, then removes the directory. Also wipes `isolate/<name>/` if present.

Refuses to remove the active profile unless `--force`.

## Switching

### `claudeorch swap <name>`

Atomically replace `~/.claude/.credentials.json` + `~/.claude.json` with the saved profile. See [ARCHITECTURE.md → Swap](ARCHITECTURE.md#swap-atomic-two-file-replacement) for the full algorithm.

Refuses with exit code **2** when any Claude session is running, unless `--force` is passed (prints a visible warning). Exit code 2 is distinct from exit 1 (generic error) so scripts can differentiate "not safe right now" from "actually broken."

### `claudeorch launch <name> [-- claude-args...]`

Materialize a per-profile isolate directory at `~/.claudeorch/isolate/<name>/` and `exec` `claude` with `CLAUDE_CONFIG_DIR` pointed at it. Use for parallel sessions across terminals.

**Flags:**
- `--isolated` skip the shared-content symlinks (no `CLAUDE.md`, `projects/`, etc. linked from `~/.claude/`). Full isolation.

**Pass-through:** arguments after `--` go straight to `claude`:
```bash
claudeorch launch work -- --dangerously-skip-permissions
```

## Token management

### `claudeorch refresh <name>`

Exchange the profile's refresh token for a new access + refresh token pair via Anthropic's OAuth endpoint. Writes the rotated tokens back to the profile atomically; if the profile is active, also syncs `~/.claude/.credentials.json` and the isolate copy (if any).

Refuses to refresh the active profile unless `--force` (the rotation invalidates the old token, which may break running sessions).

**Errors:**
- `invalid_grant` (refresh token expired or revoked) → marks `needs_reauth: true` on the profile, exits 1, instructs the user to `claude /login` and re-add.
- 5xx / network → returns a wrapped `ErrNetwork`.

## Diagnostics

### `claudeorch doctor`

Runs a set of health checks:

- `~/.claudeorch/` permissions (`0700`)
- `store.json` parses and schema version matches
- Each profile directory + credentials at `0700` / `0600`
- `claude --version` responsive (2s timeout)
- Stale session files (dead PIDs in `~/.claude/sessions/`)
- OAuth token expiry per profile
- Orphaned `.pre-swap` backup files
- `~/.claudeorch/locks/.lock` status

**Flags:**
- `--fix` repair fixable issues (chmod permissions, remove stale PID session files, clean orphan tmp-swap dirs).

Doctor is non-destructive by default.

## Statusline

### `claudeorch statusline`

Reads Claude Code's statusLine JSON on stdin, writes a colored line with the active profile indicator. Invoked automatically by Claude on every prompt refresh; not meant to be run manually.

### `claudeorch statusline install`

Writes a `statusLine` entry to `~/.claude/settings.json` pointing to the installed `claudeorch statusline` command. Uses the absolute binary path so it survives `$PATH` changes.

**Flags:**
- `--uninstall` remove the entry (only if it's ours — foreign statusLines are left alone).

## Lifecycle

### `claudeorch upgrade`

Self-update the running binary to the latest GitHub release. Fetches the latest tag, downloads the matching binary with a live progress bar (visible bar + size + speed + ETA), verifies SHA-256, and atomically replaces the running binary via POSIX rename-in-place.

**Flags:**
- `--check` dry-run: report current + latest versions without installing.
- `--to VERSION` pin to a specific tag (e.g. `--to v0.1.0`).

### `claudeorch purge`

Wipe all `claudeorch` state:
- Zero-overwrite every credential file under `~/.claudeorch/`
- Remove the entire `~/.claudeorch/` directory

`~/.claude/` is never touched. Requires interactive confirmation (`type "purge" to confirm`) unless `--force --yes` is given.

### `claudeorch uninstall`

Nuclear option. Prints a pre-flight identity summary (which account is live, which profiles will be destroyed), requires `type "uninstall"` confirmation, then:

1. Zero-overwrites + removes `~/.claudeorch/`
2. Removes the `statusLine` entry from `~/.claude/settings.json` (only if it's ours)
3. Removes the binary itself (via POSIX rename-over-running — the kernel holds the inode until exit)

**Flags:**
- `--yes` skip confirmation (required on non-TTY).
- `--keep-binary` only remove state + statusline; keep the binary installed.
- `--keep-state` only remove the binary; keep `~/.claudeorch/` and statusline.

Both `--keep-binary` and `--keep-state` together is rejected as a no-op.

If `statusLine` cleanup fails AND we're about to remove the binary, uninstall aborts BEFORE state removal — otherwise Claude Code's settings.json would point at a dead path.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | Generic error (bad input, failed operation, parse error) |
| 2 | Safety refusal (swap / remove / refresh blocked by running session) |
