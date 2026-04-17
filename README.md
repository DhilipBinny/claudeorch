# claudeorch

**A command-line account switcher for [Claude Code](https://claude.ai/code).** Save multiple Anthropic logins, swap between them in one command, or run them in parallel terminals — without re-authenticating through the browser every time.

[![GitHub release](https://img.shields.io/github/v/release/DhilipBinny/claudeorch?display_name=tag&sort=semver&color=00ADD8)](https://github.com/DhilipBinny/claudeorch/releases/latest)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/DhilipBinny/claudeorch)](https://goreportcard.com/report/github.com/DhilipBinny/claudeorch)
[![Go Version](https://img.shields.io/badge/go-1.22%2B-00ADD8)](go.mod)
[![Platforms](https://img.shields.io/badge/platforms-Linux%20%7C%20macOS-lightgrey)]()

```bash
curl -fsSL https://raw.githubusercontent.com/DhilipBinny/claudeorch/main/install.sh | sh
```

---

## What it does

Claude Code authenticates one Anthropic account at a time. If you own **two Max subscriptions**, work across multiple organizations, or rotate accounts when you hit usage limits, you're stuck running `claude /logout` → browser OAuth → `claude /login` on every switch.

**`claudeorch` fixes that.** It's a small Go CLI that safely reads and writes Claude Code's own credential files, gives you named profiles, and lets you:

- 🔁 **Switch accounts instantly** — `claudeorch swap work` atomically replaces the active credentials, no browser needed.
- 🪟 **Run accounts in parallel** — `claudeorch launch home` in a separate terminal uses `CLAUDE_CONFIG_DIR` so two accounts run simultaneously without stepping on each other.
- 📊 **See your quota live** — `claudeorch status` and `claudeorch list` show per-account 5-hour and 7-day usage bars, reset times, and active session.
- 🔒 **Stay safe** — atomic writes with rollback, refuses to swap while Claude is mid-session, zero-telemetry, strict file permissions.
- 🪶 **Zero maintenance** — one static binary, self-updates via `claudeorch upgrade`, clean uninstall.

### Live dashboard

```text
$ claudeorch status
Active profile: work (alice@example.com)
  5H  ███░░░░░░░░░░░░  29%  resets 12m
  7D  █░░░░░░░░░░░░░░   9%  resets 6d4h

Sessions: 1 running
  terminal  pid=12345  profile=work  cwd=/home/alice/project

1 other profile. Run 'claudeorch list' for all usage.
```

```text
$ claudeorch list
PROFILE   EMAIL                    ORG       5H                    5H-RESET  7D                    7D-RESET  STATUS
* work    alice@example.com        Acme      ███░░░░░░░░░░░░  29%  12m       █░░░░░░░░░░░░░░   9%  6d4h      active
  home    alice@personal.com       Personal  ██████░░░░░░░░░  41%  3h3m      ░░░░░░░░░░░░░░░   3%  6d7h
```

---

## Installation

### Recommended — one-line installer (Linux + macOS)

No Go toolchain needed.

```bash
curl -fsSL https://raw.githubusercontent.com/DhilipBinny/claudeorch/main/install.sh | sh
```

The installer:
- Detects your OS (Linux / macOS) and CPU (amd64 / arm64)
- Downloads the matching pre-built binary from the latest [GitHub Release](https://github.com/DhilipBinny/claudeorch/releases/latest)
- Verifies SHA-256 against the release checksums
- Installs to `~/.local/bin/claudeorch` (or `/usr/local/bin/claudeorch` when run as root)
- Shows a live progress bar with size, speed, and ETA

Verify:

```bash
claudeorch --version
```

### Self-upgrade

Once installed, you never need the curl installer again:

```bash
claudeorch upgrade          # pulls the latest release
claudeorch upgrade --check  # dry-run: show current + latest
claudeorch upgrade --to v0.1.0  # pin to a specific version
```

### Alternative: `go install`

```bash
go install github.com/DhilipBinny/claudeorch/cmd/claudeorch@latest
```

### Alternative: build from source

```bash
git clone https://github.com/DhilipBinny/claudeorch.git
cd claudeorch
go build -o claudeorch ./cmd/claudeorch
sudo mv claudeorch /usr/local/bin/
```

### Uninstall

```bash
claudeorch uninstall        # interactive, with pre-flight identity summary
claudeorch uninstall --yes  # non-interactive
```

Removes `~/.claudeorch/`, the statusline wiring in `~/.claude/settings.json`, and the binary itself. **Never touches `~/.claude/`** — your Claude Code login stays intact.

---

## Quick start

```bash
# You're already logged in to Claude Code as account A (e.g. work).
# Save it as a profile FIRST — before logging out, or the tokens are gone.
claudeorch add work

# Now safely switch.
claude /logout
claude /login                     # log in as account B (home)
claudeorch add home

# From here on, switching is one command:
claudeorch swap home              # live ~/.claude/ now = home
claudeorch swap work              # back to work

# Or run both at the same time, one per terminal:
# Terminal A:
claudeorch launch work
# Terminal B:
claudeorch launch home
```

### ⚠️ The golden rule

**Always run `claudeorch add <name>` BEFORE `claude /logout` or `/login` for a different account.** Logging out deletes the local OAuth tokens — if `claudeorch` hasn't snapshotted them first, they're gone and you'll re-authenticate through the browser to recover that account.

The CLI refuses dangerous combinations with a clear error, but saving first is the rule of thumb.

---

## Commands

| Command | Purpose |
|---|---|
| `claudeorch add <name>` | Snapshot the current Claude login as a named profile. |
| `claudeorch list` | Table of all profiles with live 5H/7D usage bars. |
| `claudeorch status` | Dashboard: active profile, usage, running sessions. |
| `claudeorch swap <name>` | Atomically switch the default account in `~/.claude/`. |
| `claudeorch launch <name>` | Run `claude` with a per-profile `CLAUDE_CONFIG_DIR` (parallel sessions). |
| `claudeorch refresh <name>` | Rotate OAuth tokens against Anthropic's token endpoint. |
| `claudeorch rename <old> <new>` | Rename a profile. |
| `claudeorch remove <name>` | Remove a profile (zero-overwrites credentials). |
| `claudeorch doctor` | Diagnostic checks; `--fix` repairs common issues. |
| `claudeorch statusline install` | Wire a profile-aware statusline into Claude Code. |
| `claudeorch upgrade` | Self-update to the latest release. |
| `claudeorch purge` | Wipe all `claudeorch` state (profiles, logs, locks). |
| `claudeorch uninstall` | Remove state + statusline + binary. |

Run `claudeorch <command> --help` for flags and options.

---

## How it works

Claude Code stores authentication in two files:

- `~/.claude/.credentials.json` — OAuth access + refresh tokens
- `~/.claude.json` — account identity (email, organization)

`claudeorch` treats these as the canonical state. Each `add` snapshots the pair into `~/.claudeorch/profiles/<name>/`. A `swap` is a lock-protected, two-file atomic rename with backup — if the second rename fails, the first is rolled back, and the `doctor` command can recover from crashes.

`launch` takes a different approach: instead of mutating `~/.claude/`, it materializes a per-profile directory in `~/.claudeorch/isolate/<name>/` with fresh credential copies and symlinks for shared content (`CLAUDE.md`, `projects/`, `skills/`), then `exec`s `claude` with `CLAUDE_CONFIG_DIR` pointed at it. Same brain, different login — runnable in parallel without conflict.

Every credential write goes through an atomic `temp + fsync + rename + parent-dir fsync` primitive. Every mutating command holds a POSIX `flock(2)` on `~/.claudeorch/locks/.lock` so two `claudeorch` invocations can't interleave. Credentials are zero-overwritten before deletion.

---

## Safety & privacy

- **Zero telemetry.** Three HTTPS endpoints are ever contacted, all user-triggered: Anthropic's OAuth token endpoint (for `refresh`), Anthropic's usage API (for `list` / `status`), and GitHub's releases API (for `upgrade`).
- **Strict permissions.** `0700` on all directories, `0600` on credential files — enforced on every write, audited by `doctor`.
- **Token redaction.** Debug logs (`--debug`) pass every token through a first-10-char redactor. Raw tokens never hit disk or stderr.
- **Safety gates.** Destructive ops (`swap`, `refresh`, `remove`) refuse while Claude sessions are running unless `--force` is passed.
- **Atomic writes.** Credential files are never left partial — crash or power loss leaves the old content intact.
- **Never touches `~/.claude/` unless you ask.** `purge` and `uninstall` wipe `~/.claudeorch/` only.

---

## FAQ

### Will this get my Claude account banned?

No. `claudeorch` only reads and writes the same files Claude Code itself writes, using Anthropic's own OAuth refresh endpoint for token rotation. It's not a proxy, not a wrapper, not an API scraper. It sits on disk, not on the wire.

### Can I run two Claude Code sessions at the same time under different accounts?

Yes — that's `claudeorch launch`. Each launched session gets its own `CLAUDE_CONFIG_DIR` so the two instances never share credentials or session state.

### What happens to my existing Claude Code setup?

Nothing breaks. `claudeorch` snapshots your current login once (`claudeorch add`), then treats it as one of its managed profiles. You can uninstall `claudeorch` any time — `~/.claude/` stays exactly as it was.

### Does it work with Claude API keys (not Claude Code)?

No. `claudeorch` is specifically for **Claude Code** (the `claude` CLI that uses OAuth, installed via npm or the official installer). For plain API keys, use environment variables.

### Which accounts does it support?

Any Claude Code account — Max, Pro, API-subscription-linked. Org-scoped identities work too (multiple organizations under one email).

### Does it support Windows?

Not yet. Linux and macOS are first-class today. Windows is planned for a future release — use WSL2 in the meantime.

### Is it safe to `claudeorch upgrade`?

Yes. The binary is replaced atomically via POSIX rename (the running process keeps its inode until exit), and every download is SHA-256 verified against the release's checksums.

---

## Platform support

- **Linux** (x86_64, arm64) — tier 1
- **macOS** (Intel, Apple Silicon) — tier 1
- **Windows** — planned (use WSL2 today)

Requires [Claude Code](https://claude.ai/code) installed and logged in at least once.

---

## Contributing

Bug reports, feature requests, and PRs welcome:

- 🐛 [Open an issue](https://github.com/DhilipBinny/claudeorch/issues) for bugs or compatibility reports
- 💬 [Start a discussion](https://github.com/DhilipBinny/claudeorch/discussions) for design feedback
- ⭐ Star the repo to follow progress

---

## Disclaimer

`claudeorch` is a third-party tool. It is **not affiliated with, endorsed by, or sponsored by Anthropic**. It interoperates with Claude Code's on-disk file formats and OAuth endpoints and may break if Anthropic changes either — fixes land quickly when that happens.

---

## License

[MIT](LICENSE) © 2026 Dhilip Binny

---

## Keywords

*Claude Code multiple accounts, Claude Code account switcher, Claude Code profile manager, Claude Max multiple subscriptions, Anthropic CLI multi-account, Claude Code parallel sessions, OAuth token manager Claude, Claude Code usage monitor.*
