//go:build embedspa

// Package vellum embeds the built SPA into the binary (PHY-116).
// Build with `-tags embedspa` after `npm run build` in web/ — the release
// Dockerfile does both; a plain `go build` skips the embed so the Go
// toolchain never depends on node.
package vellum

import (
	"embed"
	"io/fs"
)

//go:embed all:web/dist
var dist embed.FS

// DistFS returns the embedded SPA filesystem rooted at the dist directory.
func DistFS() fs.FS {
	sub, err := fs.Sub(dist, "web/dist")
	if err != nil {
		panic(err) // embed layout is fixed at compile time
	}
	return sub
}
