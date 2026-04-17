# claudeorch

> Switch, isolate, and track usage across multiple Claude Code accounts.

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Status: pre-release](https://img.shields.io/badge/status-pre--release-orange)]()
[![Go: 1.22+](https://img.shields.io/badge/go-1.22%2B-00ADD8)]()

A cross-platform Go CLI for running multiple Claude Code subscriptions on one machine — credential swap, parallel isolated sessions, and usage monitoring, all without repeated logins.

> **Status:** pre-release, active development. Not yet installable. Star the repo to follow progress.

---

## Why `claudeorch`?

Claude Code supports one authenticated account per user at a time. If you have two Max subscriptions, a work + personal split, or want to rotate accounts when usage limits hit, you're stuck logging out and back in.

`claudeorch` solves that with three core capabilities:

- **Swap** — replace the active Claude Code account atomically. Safe: refuses to run while a Claude session is active.
- **Isolate** — launch `claude` with a per-account configuration directory, so different terminals can run different accounts in parallel.
- **Track** — show 5-hour and 7-day usage percentages per account, so you know when to swap.

## Features

### v1

- [x] Register multiple Claude Code accounts (`claudeorch add`)
- [x] Swap the active account in place (`claudeorch swap`)
- [x] Launch isolated parallel sessions (`claudeorch launch`)
- [x] List profiles with per-account usage (`claudeorch list`)
- [x] Status, rename, remove, refresh, doctor commands
- [x] Atomic credential operations with rollback
- [x] Running-session detection (blocks unsafe swaps)
- [x] Shell completion (bash, zsh, fish, PowerShell)
- [x] Signed release binaries (Homebrew, `go install`, curl installer)

### v1.1 (coming later)

- [ ] `claudeorch watch` — auto-rotate accounts on usage threshold
- [ ] Windows (Tier 1) support

### Explicitly not planned

- ❌ Credential export/import (see design rationale)
- ❌ OAuth proxy / API interception
- ❌ Multi-provider support (Claude only)
- ❌ Telemetry of any kind

## Install

### From source (Go 1.22+)

```bash
git clone https://github.com/DhilipBinny/claudeorch.git
cd claudeorch
go build -o claudeorch ./cmd/claudeorch
sudo mv claudeorch /usr/local/bin/   # or anywhere on your PATH
claudeorch --version
```

### Planned release paths (coming with v0.1.0)

```bash
# Go toolchain
go install github.com/DhilipBinny/claudeorch@latest

# Homebrew
brew install DhilipBinny/claudeorch/claudeorch

# curl
curl -fsSL https://raw.githubusercontent.com/DhilipBinny/claudeorch/main/install.sh | sh
```

## Usage

### ⚠️ The golden rule

**Run `claudeorch add <name>` BEFORE you `claude /logout` or `/login` for a different account.**

`claude /logout` deletes the local OAuth tokens on disk. If you haven't saved them first via `add`, they are **gone** — you will have to re-authenticate through the browser to get that account back, and any work-in-progress sessions using those tokens may fail.

### First-time setup

```bash
# You're logged in as account A (say, work). Save it FIRST.
claudeorch add work

# Now it's safe to switch.
claude /logout
claude /login                            # log in as account B (home)

# Save account B too.
claudeorch add home

# From here on, claudeorch has both. You can switch freely.
```

### Daily use

```bash
# See who you've saved, who's active, and how much quota is left
claudeorch list

# Swap the default account (affects future plain 'claude' invocations)
claudeorch swap home

# Parallel sessions — each terminal runs a different account simultaneously
terminal 1 $ claudeorch launch work
terminal 2 $ claudeorch launch home

# Rotate OAuth tokens (when running close to expiry)
claudeorch refresh work

# Health check
claudeorch doctor
```

### Adding a new account later

```bash
# 1. Your current live account MUST already be saved (otherwise you'll lose it).
claudeorch list                          # verify current account is listed

# 2. Log in as the new account.
claude /logout
claude /login                            # browser OAuth flow as the new account

# 3. Save the new account. Pick a name.
claudeorch add <newname>
```

**If `claudeorch add <newname>` errors with "live ~/.claude/ holds account X, which is already saved as Y":** that means the account you just logged in as is already saved under a different name. Either use that existing name, or log in as a truly new account.

### Removing / renaming

```bash
claudeorch rename old new                # rename a profile
claudeorch remove work                   # remove a profile (refuses if active without --force)
claudeorch --force remove work           # remove the active profile (zero-overwrites credentials first)
```

### Nuclear reset

```bash
claudeorch purge                         # interactive confirmation
claudeorch --force purge --yes           # non-interactive — wipes all claudeorch state
```

`purge` never touches `~/.claude/` — only `~/.claudeorch/`.

Expected output:

```
PROFILE   EMAIL                   5H                    7D                    RESET
────────────────────────────────────────────────────────────────────────────────────
● work    alice@example.com       ████████████░░░  84%   ██████░░░░░░░░░  42%   2h 15m
  home    alice@personal.dev      ██░░░░░░░░░░░░░  12%   █░░░░░░░░░░░░░░   8%   4h 48m
```

## How it works

`claudeorch` reads and writes the files Claude Code uses for authentication (`~/.claude/.credentials.json` and `~/.claude.json`). Each registered account gets a private snapshot in `~/.claudeorch/profiles/`. Swapping is an atomic, lock-protected rename; launching isolated uses `CLAUDE_CONFIG_DIR` to point `claude` at a per-account directory.

Shared memory (your global `CLAUDE.md` and project history) is symlinked across isolated sessions — same brain, different logins. Credentials, identity, and per-session caches stay per-account.

## Safety

- **Atomic writes with rollback** — credentials never end up in a mismatched state, even on crash or power loss.
- **Session detection** — refuses to swap while any Claude session is running in the default scope, preventing silent identity bleed.
- **Strict file permissions** — 0700 on directories, 0600 on credential files. `claudeorch doctor` enforces.
- **Zero telemetry** — no analytics, no phone-home. Three HTTPS calls total, all Anthropic, all user-triggered.

## Compatibility

- **Linux** (x86_64, arm64) — Tier 1
- **macOS** (Apple Silicon + Intel) — Tier 1
- **Windows** — coming in v1.x. Use WSL2 in the meantime.
- **Requires:** Claude Code installed and logged in at least once.

## Disclaimer

This is a third-party tool. It reverse-engineers Claude Code's on-disk file formats and may break when Anthropic updates Claude Code. Fixes released as quickly as possible — open an issue if something breaks. Not affiliated with or endorsed by Anthropic.

## Contributing

Once v0.1.0 ships, contributions welcome. For now:

- ⭐ Star the repo to follow progress
- 🐛 Open an issue for feature requests or compatibility reports
- 💬 Start a discussion for design feedback

## License

[MIT](LICENSE) © 2026 Dhilip Binny
