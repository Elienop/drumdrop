//go:build webui

// Package web embeds the built single-page UI (web/dist) under the `webui` build
// tag. The default (untagged) build uses web_stub.go instead, so `go build ./...`
// stays green without a frontend build.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// DistFS returns the built SPA tree rooted at dist/ (index.html at the root).
func DistFS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // dist is embedded at compile time; a Sub failure is a build bug.
	}
	return sub
}
