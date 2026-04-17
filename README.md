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

## Usage (preview)

```bash
# First-time setup
claude /login                            # log in with account 1
claudeorch add work

claude /logout && claude /login          # log in with account 2
claudeorch add home

# Parallel sessions — each terminal, its own account
terminal 1 $ claudeorch launch work
terminal 2 $ claudeorch launch home

# Swap in place when tokens burn
claudeorch swap home

# See who's active and how much quota is left
claudeorch list
```

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
