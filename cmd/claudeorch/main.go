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
	root := newRootCmd()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
