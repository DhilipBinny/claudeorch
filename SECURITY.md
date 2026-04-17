# Security

`claudeorch` handles OAuth tokens that control access to Anthropic subscriptions. Security is not optional.

## Reporting a vulnerability

Please **do not** open a public issue for security reports. Email the maintainer directly via the address listed on the [GitHub profile](https://github.com/DhilipBinny), or use [GitHub's private vulnerability reporting](https://github.com/DhilipBinny/claudeorch/security/advisories/new).

You'll get an acknowledgement within 72 hours and a public fix within 7 days of triage, with a coordinated disclosure window if the report is embargoed elsewhere.

## Threat model

`claudeorch` assumes:

- **Trusted local user.** If an attacker has read access to your home directory, they already have your Claude credentials — this tool does not protect against that.
- **Trusted network to Anthropic.** We use HTTPS with the Go stdlib's default certificate verification. No custom CA pinning.
- **Single-user machine, or proper Unix permissions.** Files are `0600`; directories `0700`. Other local users cannot read your profiles unless they have elevated privileges.

Out of scope: keylogger protection, OS-level exfiltration, compromised CA roots.

## Security-relevant design choices

### Zero telemetry

Three HTTPS endpoints are ever contacted, all user-triggered:

| Endpoint | When | Purpose |
|---|---|---|
| `platform.claude.com/v1/oauth/token` | `claudeorch refresh` | Rotate OAuth access + refresh tokens |
| `api.anthropic.com/api/oauth/usage` | `claudeorch list` / `status` | Fetch 5H / 7D usage counters |
| `api.github.com/repos/DhilipBinny/claudeorch/releases` | `claudeorch upgrade` | Resolve latest release tag |

No analytics, no crash reporting, no auto-update pings, no usage metrics. No background timers — every request happens because you ran a command.

### File permissions

- All `claudeorch`-created directories: `0700` (owner rwx, group/other none)
- All credential files: `0600` (owner rw, group/other none)
- Enforced on every write via `fsio.EnsureDir` / `fsio.WriteFileAtomic`
- Audited by `claudeorch doctor`; `--fix` repairs drift

### Atomic writes

Every credential write uses the temp-and-rename pattern:

1. Create temp file in the same directory as the target (same-filesystem, so rename is atomic)
2. Write data, `fsync(2)` the temp fd
3. `chmod(2)` to the requested permission
4. `close(2)` the fd
5. `rename(2)` temp → target (atomic on POSIX same-fs)
6. `fsync(2)` the parent directory (durable rename metadata)

Failure between steps 1–5 removes the temp; failure at step 6 is loudly surfaced (data is already in place but not fsynced).

### Two-file swap

`swap` must replace `.credentials.json` and `.claude.json` together atomically — the identity file and the tokens must stay consistent. The algorithm:

1. **Stage** — copy profile files to `tmp-swap-<pid>/`
2. **Backup** — rename live files to `*.pre-swap`
3. **Commit A** — rename staged credentials into place
4. **Commit B** — rename staged `claude.json` into place
5. **Cleanup** — remove backups + tmp dir

If Commit B fails, **rollback** restores both `.pre-swap` backups. If the process crashes before cleanup, `claudeorch doctor` recovers the orphaned `tmp-swap-*/` and surfaces `.pre-swap` files for manual inspection.

### Locking

Every state-mutating command (`add`, `remove`, `rename`, `swap`, `refresh`, `purge`) holds a POSIX `flock(2)` on `~/.claudeorch/locks/.lock` with a 3-second timeout. Two concurrent `claudeorch` invocations cannot interleave writes.

### Zero-overwrite on deletion

When `remove`, `purge`, or `uninstall` delete credential files, they are **overwritten with zeros** before the filesystem unlink. This makes post-deletion forensic recovery harder (though a determined attacker with journaled-filesystem access may still recover prior versions — treat `claudeorch remove` as "rotate these tokens soon").

### Token redaction in logs

Every debug log line (`--debug`) passes tokens through `log.Redact()`, which truncates to the first 10 characters wrapped in `<redacted:...>`. A belt-and-suspenders `log.ScanAndRedact()` catches stray token patterns in free-form text. The redaction test suite greps the debug output of every command for raw token shapes (`sk-ant-*`, `ref_*`, `eyJ*`) and fails if any survive.

### Session-aware safety gates

`swap`, `remove`, and `refresh` refuse to run while any Claude session is detected in `~/.claude/sessions/` or `~/.claude/ide/`. `--force` overrides with a visible warning. This prevents silent identity bleed into a running session.

### Never touches `~/.claude/` unintentionally

`purge` and `uninstall` wipe `~/.claudeorch/` only. `~/.claude/` is Claude Code's territory — we never delete or mutate it except via the documented `swap` and `refresh` operations. Tests pin this invariant.

### Supply-chain

- Pre-built binaries are **reproducibly built** with `CGO_ENABLED=0`, `-trimpath`, and fixed `-ldflags`. Two builds from the same commit produce byte-identical binaries.
- Every release includes a `SHA256SUMS` file.
- `install.sh` and `claudeorch upgrade` verify SHA-256 against `SHA256SUMS` before installing.
- The release workflow runs from a known commit; the pre-built binary matches the source tree at that commit tag.

## What this tool does NOT do

- No credential export to other machines (intentional — DESIGN §16 rejects it)
- No import of credentials from external sources
- No proxy / MITM / API interception
- No background process
- No auto-rotate on usage threshold (planned for a future release)
