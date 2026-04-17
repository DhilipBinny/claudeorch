package main

// Build-time variables, populated via -ldflags "-X main.Version=... -X main.Commit=... -X main.BuildDate=...".
//
// These default to "dev" placeholders when built without ldflags (e.g., `go build` locally).
var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)
