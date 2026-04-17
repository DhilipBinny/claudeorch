# claudeorch

**A command-line account switcher for [Claude Code](https://claude.ai/code).** Save multiple Anthropic logins, swap between them in one command, or run them in parallel terminals — without re-authenticating through the browser.

[![GitHub release](https://img.shields.io/github/v/release/DhilipBinny/claudeorch?display_name=tag&sort=semver&color=00ADD8)](https://github.com/DhilipBinny/claudeorch/releases/latest)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/DhilipBinny/claudeorch)](https://goreportcard.com/report/github.com/DhilipBinny/claudeorch)
[![Platforms](https://img.shields.io/badge/platforms-Linux%20%7C%20macOS-lightgrey)]()

```bash
curl -fsSL https://raw.githubusercontent.com/DhilipBinny/claudeorch/main/install.sh | sh
```

---

## What it does

Claude Code authenticates one Anthropic account at a time. With two Max subscriptions or multiple orgs, you end up cycling through `claude /logout` → browser OAuth → `claude /login` every time you switch.

`claudeorch` saves named profiles and gives you:

- 🔁 **Instant switching** — `claudeorch swap work`, no browser.
- 🪟 **Parallel sessions** — `claudeorch launch home` in a separate terminal runs alongside without conflict.
- 📊 **Live usage** — 5H + 7D quota bars per account, straight from Anthropic's usage API.
- 🔒 **Safe by default** — atomic writes, session-aware gates, strict permissions, zero telemetry.

```text
$ claudeorch status
Active profile: work (alice@example.com)
  5H  ███░░░░░░░░░░░░  29%  resets 12m
  7D  █░░░░░░░░░░░░░░   9%  resets 6d4h

Sessions: 1 running
  terminal  pid=12345  profile=work  cwd=/home/alice/project

1 other profile. Run 'claudeorch list' for all usage.
```

---

## Install

**One-liner (Linux + macOS):**

```bash
curl -fsSL https://raw.githubusercontent.com/DhilipBinny/claudeorch/main/install.sh | sh
```

Detects your OS + CPU, downloads the matching binary from the latest [release](https://github.com/DhilipBinny/claudeorch/releases/latest), SHA-256 verifies it, and installs to `~/.local/bin/claudeorch`.

Once installed, update with `claudeorch upgrade`.

Alternatives: `go install github.com/DhilipBinny/claudeorch/cmd/claudeorch@latest`, or clone + `go build`.

---

## Quick start

```bash
# You're logged into Claude Code as account A (e.g. work).
# Save it FIRST — before logging out, or the tokens vanish.
claudeorch add work

# Now switch safely.
claude /logout
claude /login                     # as account B (home)
claudeorch add home

# From here on, switching is one command:
claudeorch swap home              # live ~/.claude/ now = home
claudeorch swap work              # back to work

# Or run both in parallel, one per terminal:
claudeorch launch work    # terminal A
claudeorch launch home    # terminal B
```

> **Golden rule:** always `claudeorch add` *before* `claude /logout`. Logging out deletes the local tokens.

---

## Commands

`add`, `list`, `status`, `swap`, `launch`, `refresh`, `rename`, `remove`, `doctor`, `statusline`, `upgrade`, `purge`, `uninstall`.

Run `claudeorch <command> --help` for flags, or see the full [commands reference](docs/COMMANDS.md).

---

## FAQ

**Will this get my account banned?** No. `claudeorch` only reads and writes the same files Claude Code writes, using Anthropic's own OAuth refresh endpoint. It's not a proxy, not a wrapper, not a scraper.

**Does it support Windows?** Not yet. Linux and macOS today; Windows is planned. Use WSL2 in the meantime.

**Does it work with Claude API keys?** No — this is for Claude Code's OAuth flow. Plain API keys use environment variables.

---

## Documentation

- **[Commands reference](docs/COMMANDS.md)** — every subcommand, flag, and exit code
- **[Architecture](docs/ARCHITECTURE.md)** — how the atomic swap, isolation, and OAuth refresh actually work
- **[Security](SECURITY.md)** — threat model, crypto choices, vulnerability reporting

---

## Contributing

- 🐛 [Report a bug](https://github.com/DhilipBinny/claudeorch/issues)
- 💬 [Start a discussion](https://github.com/DhilipBinny/claudeorch/discussions)
- ⭐ Star the repo to follow progress

---

## Disclaimer

`claudeorch` is a third-party tool. It is **not affiliated with, endorsed by, or sponsored by Anthropic**. It interoperates with Claude Code's on-disk file formats and OAuth endpoints and may break if Anthropic changes either — fixes land quickly when that happens.

## License

[MIT](LICENSE) © 2026 Dhilip Binny
