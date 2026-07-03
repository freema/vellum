//go:build !embedspa

// Package vellum embeds the built SPA into the binary (PHY-116). This stub
// keeps plain `go build ./...` working without node — the server then runs
// API+MCP only, with no web UI.
package vellum

import "io/fs"

// DistFS returns nil: no SPA in this build.
func DistFS() fs.FS {
	return nil
}
