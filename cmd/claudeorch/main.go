// Package main is the entrypoint for the claudeorch CLI.
//
// claudeorch manages multiple Claude Code accounts on one machine:
// credential swap, parallel isolated sessions, and usage monitoring.
package main

import (
	"fmt"
	"os"
)

func main() {
	args := os.Args[1:]
	if len(args) == 1 && (args[0] == "--version" || args[0] == "-v") {
		fmt.Printf("claudeorch %s (commit: %s, built: %s)\n", Version, Commit, BuildDate)
		return
	}
	fmt.Fprintln(os.Stderr, "claudeorch: command framework not yet wired (see commit 2)")
	os.Exit(1)
}
