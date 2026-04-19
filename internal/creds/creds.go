// Package creds provides a platform-aware interface for reading and writing
// Claude Code's "live" OAuth credentials — the ones Claude itself actively
// uses and rotates.
//
// Why this package exists
//
// On Linux, Claude Code stores credentials at ~/.claude/.credentials.json
// as a plain JSON file. On macOS, they're in the system Keychain under
// service name "Claude Code-credentials" (account = macOS username), and
// Claude Code actively deletes any .credentials.json file it finds on disk
// (see github.com/anthropics/claude-code/issues/1414).
//
// claudeorch needs to read these credentials on 'add' (snapshot the current
// account), compare them during 'reconcile' (detect drift), and write them
// on 'swap' (switch the active account) and 'refresh' (rotate tokens).
// This package hides the platform difference behind ReadLive/WriteLive so
// callers don't need build-tag spaghetti.
//
// Platform implementations:
//
//	creds_linux.go  — reads/writes ~/.claude/.credentials.json via fsio
//	creds_darwin.go — reads/writes macOS Keychain via the 'security' CLI
//
// Keychain details (verified against a real macOS machine, 2026-04-19):
//
//	Service:  "Claude Code-credentials"
//	Account:  macOS username (e.g. "binny")
//	Format:   identical JSON blob to Linux's .credentials.json
//	Keychain: ~/Library/Keychains/login.keychain-db
package creds
