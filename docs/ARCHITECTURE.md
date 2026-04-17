# Architecture

How `claudeorch` actually works under the hood.

## Claude Code's on-disk layout

Claude Code stores its authentication and identity across two files at well-known paths:

| Path | Contents |
|---|---|
| `~/.claude/.credentials.json` | OAuth access + refresh tokens, scopes, subscription type |
| `~/.claude.json` | Account identity (email, organization UUID, org name), plus a large pile of session history and settings |

`CLAUDE_CONFIG_DIR` overrides the default `~/.claude/` root, with one important asymmetry: when the env var is set, `.claude.json` moves *inside* the config dir; when it's unset, `.claude.json` is at `$HOME/.claude.json` (not inside `~/.claude/`). `claudeorch` replicates this exactly — getting it wrong is a major footgun.

## Profile model

Each profile is a snapshot of `(.credentials.json, .claude.json)` at a point in time, saved under:

```
~/.claudeorch/
├── store.json                       # index: version, active pointer, profile metadata
├── profiles/
│   ├── work/
│   │   ├── credentials.json         # 0600 — snapshot of ~/.claude/.credentials.json
│   │   └── claude.json              # 0600 — snapshot of ~/.claude.json
│   └── home/...
├── isolate/                         # materialized per-profile dirs for 'launch'
│   ├── work/
│   │   ├── .credentials.json        # 0600 — copy, not symlink
│   │   ├── .claude.json             # 0600 — copy
│   │   ├── CLAUDE.md                # → symlink to ~/.claude/CLAUDE.md
│   │   ├── projects/                # → symlink
│   │   ├── skills/                  # → symlink
│   │   ├── settings.json            # → symlink
│   │   └── settings.local.json      # copy (can contain per-session state)
│   └── home/...
├── locks/.lock                      # POSIX flock target
└── log/claudeorch.log               # rotated via lumberjack
```

Directories are `0700`, credential files `0600`.

## Swap: atomic two-file replacement

`swap` is the riskiest operation — if it fails halfway, you're stuck between two identities. The algorithm is deliberately simple and rollback-safe:

```
1. Stage       tmp-swap-<pid>/credentials.json       (copy from profile)
               tmp-swap-<pid>/claude.json            (copy from profile)
2. Backup      mv  ~/.claude/.credentials.json       → *.pre-swap
               mv  ~/.claude.json                    → *.pre-swap
3. Commit-A    mv  tmp-swap-<pid>/credentials.json   → ~/.claude/.credentials.json
4. Commit-B    mv  tmp-swap-<pid>/claude.json        → ~/.claude.json
5. Cleanup     rm  *.pre-swap; rm -r tmp-swap-<pid>/
```

**Failure modes:**

- Stage fails → tmp dir removed, live state untouched, error returned.
- Backup fails → whichever backup succeeded is restored, tmp removed.
- Commit-A fails → rollback: restore both `.pre-swap` backups.
- Commit-B fails → rollback: restore both `.pre-swap` backups (commit-A is reverted by restoring the creds backup over the new creds).
- Crash anywhere → `tmp-swap-<pid>/` and `*.pre-swap` files are left on disk; `claudeorch doctor` reports them and cleans orphans where the owning PID is dead.

**Why both files?** The tokens and the identity must stay consistent. Swapping tokens without identity would leave Claude Code thinking it's talking to account A with account B's access token — undefined behavior on the server side.

## Launch: isolated materialization

`launch` avoids mutating `~/.claude/` entirely. Instead, it materializes a per-profile directory and execs `claude` with `CLAUDE_CONFIG_DIR` pointed at it. The materialization is idempotent — broken symlinks are repaired on every invocation.

**Layout decisions:**

- Credentials: **copied** (not symlinked). Symlinking would make all isolate dirs share the same on-disk file, defeating the point.
- Identity (`.claude.json`): **copied**. Same reason.
- `CLAUDE.md`, `projects/`, `skills/`, `settings.json`, `statusline-command.sh`: **symlinked** from `~/.claude/`. Shared memory and project context should be identical across accounts.
- `settings.local.json`: **copied**, not symlinked. May contain per-session state.

After materialization, `launch` releases the global lock and `syscall.Exec`s `claude`. Because `Exec` replaces the process image, `defer`s in Go don't run — the code explicitly flushes logs and releases the lock **before** exec.

## Atomic write primitive

Every credential write uses this pattern:

```go
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
    // 1. Create temp file in the SAME directory (same-filesystem = atomic rename).
    tmp, _ := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
    // 2. Write + fsync data.
    tmp.Write(data); tmp.Sync()
    // 3. Chmod to requested perm.
    tmp.Chmod(perm)
    tmp.Close()
    // 4. Atomic rename over the target.
    os.Rename(tmp.Name(), path)
    // 5. Fsync the parent dir to durably record the rename.
    d, _ := os.Open(filepath.Dir(path))
    d.Sync(); d.Close()
    return nil
}
```

Any failure between steps 1–4 removes the temp file. Cross-device rename (EXDEV, detected via `errors.Is(err, syscall.EXDEV)`) returns a distinct `ErrCrossDevice` — we refuse to fall back to copy+delete because that loses atomicity.

## Locking

A single global POSIX `flock(2)` at `~/.claudeorch/locks/.lock` serializes every state-mutating command. The implementation uses non-blocking `LOCK_EX | LOCK_NB` in a 50ms poll loop until a 3-second deadline, so the lock respects context timeouts instead of blocking forever.

The FD stays open for the duration of the lock — `flock` is FD-bound, not path-bound, so closing the FD releases the lock immediately. The release function returned by `AcquireLock` is a closure over the FD.

## OAuth refresh

`refresh` calls Anthropic's token endpoint and merges the response back into the original credentials blob via `map[string]any` round-trip. This matters because real `.credentials.json` files contain fields we don't know about (`rateLimitTier`, `subscriptionType`, bonus scopes, and whatever Anthropic adds tomorrow). A narrow typed struct would drop them silently on write.

The refresh also preserves the *shape* of `expiresAt`: real Claude Code files use milliseconds-since-epoch as a JSON number, but older drafts used RFC3339 strings. We detect the original type on read and write back in the same shape, so our refresh output never looks different to Claude Code than Claude Code's own refresh output.

## Usage API

`list` and `status` fetch per-profile usage from `api.anthropic.com/api/oauth/usage`. Shape:

```json
{
  "five_hour":  { "utilization": 9.0, "resets_at": "2026-04-17T16:00:00.699600+00:00" },
  "seven_day":  { "utilization": 7.0, "resets_at": "2026-04-23T20:00:00.699617+00:00" },
  "seven_day_sonnet": { ... },
  "extra_usage": { "is_enabled": true, "monthly_limit": 7000, "used_credits": 100.0, ... }
}
```

`utilization` is a percentage (0-100), which we normalize to 0.0-1.0 for rendering. Clamped at both ends. `resets_at` parses as RFC3339Nano to tolerate both microsecond precision and plain RFC3339.

## Self-upgrade

`upgrade` resolves the latest release tag via GitHub's `/releases` endpoint (not `/releases/latest`, to include pre-releases), downloads the matching binary with a live progress bar, SHA-256 verifies against the release's `SHA256SUMS`, and atomically replaces the running binary via POSIX rename-in-place. The kernel holds the running inode until the current process exits, so the replaced binary takes effect on the next invocation.

## Recovery / doctor

`doctor` runs a set of checks that can each be repaired independently:

- Directory permissions (0700)
- Profile file permissions (0600)
- `claude --version` responsive (2s timeout)
- Stale session files (dead-PID entries in `~/.claude/sessions/`)
- OAuth token expiry per profile
- Orphaned `.pre-swap` backups
- Orphaned `tmp-swap-*/` directories from crashed swaps
- `store.json` schema version

`--fix` applies safe repairs (chmod, remove stale PID files, clean dead-PID tmp dirs). Ambiguous cases (`.pre-swap` orphans — may indicate a half-completed swap) are surfaced but not auto-repaired.

## Dependencies

- `github.com/spf13/cobra` — command framework
- `github.com/fatih/color` — ANSI coloring
- `gopkg.in/natefinch/lumberjack.v2` — log rotation
- `golang.org/x/sys` — flock, termios, process checks

No database, no TUI library, no crypto beyond stdlib, no third-party HTTP client.

## Build

Binaries are built with `CGO_ENABLED=0 -trimpath -ldflags "-X main.Version=... -s -w"`. All four target binaries (linux/amd64, linux/arm64, darwin/amd64, darwin/arm64) cross-compile from a single host.
